package server

// WebSocket 事件推送（设计 20）：GET /ws，Web 管理员会话可用，面板向页面推送事件；页面不通过它发送指令。
// 只实现面板需要的 RFC 6455 子集（不引入依赖）：握手、服务端发文本帧、ping / pong、关闭；
// 客户端发来的数据帧读取后丢弃。
//
// 【安全】
//   - 凭会话 Cookie 认证（经 admin 中间件），并校验 Origin 与面板同源：浏览器跨站发起 WebSocket 时会自动带上
//     Cookie，不校验 Origin 就会被其他网站借用登录状态（跨站 WebSocket 劫持）。缺少 Origin 时拒绝（失败即关闭，设计 43.1）
//   - 每 30 秒重新校验会话：退出登录、被踢出或过期后连接随即关闭
//   - 连接总数有上限；客户端消费过慢（发送队列满）时断开，不让慢连接拖住推送
//   - 事件只含页面已有权限读取的数据，不含任何凭证

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	wsGUID         = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsMaxClients   = 256              // 面板全部 WebSocket 连接的上限
	wsSendQueue    = 64               // 每个连接的待发送事件数，满了说明客户端过慢，断开
	wsReadTimeout  = 75 * time.Second // 超过此时间没有收到任何帧（含 pong）视为断开
	wsWriteTimeout = 10 * time.Second
	wsMaxFrame     = 4 << 10 // 客户端帧的上限；页面只回 pong 与关闭帧
)

// wsEvent 是推送给页面的一条事件（设计 20）。字段与 api/openapi.yaml 的 WsEvent 一致。
type wsEvent struct {
	Type     string `json:"type"`
	ServerID int64  `json:"server_id,omitempty"`
	TS       int64  `json:"ts"`
	Data     any    `json:"data,omitempty"`
}

// 事件类型
const (
	evServerEnrolled = "server.enrolled" // 主机已用注册码注册（设计 19.11、27.6）
)

type wsClient struct {
	send chan []byte
	done chan struct{}
	once sync.Once
}

func (c *wsClient) close() { c.once.Do(func() { close(c.done) }) }

// wsHub 管理全部连接，把事件广播给每个连接。
type wsHub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

func newWSHub() *wsHub { return &wsHub{clients: map[*wsClient]struct{}{}} }

func (h *wsHub) add() *wsClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= wsMaxClients {
		return nil
	}
	c := &wsClient{send: make(chan []byte, wsSendQueue), done: make(chan struct{})}
	h.clients[c] = struct{}{}
	return c
}

func (h *wsHub) remove(c *wsClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
}

func (h *wsHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// closeAll 关闭全部连接（面板退出时）。
func (h *wsHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		delete(h.clients, c)
		c.close()
	}
}

// publish 把事件发给所有连接；不阻塞：队列已满的连接被断开（页面会自动重连并重新拉取数据）。
func (h *wsHub) publish(ev wsEvent) {
	if ev.TS == 0 {
		ev.TS = time.Now().Unix()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- b:
		default:
			delete(h.clients, c)
			c.close()
		}
	}
}

// sameOrigin 判断 WebSocket 握手的 Origin 是否为面板自身：与请求的 Host 相同，或与 --public-url 的主机相同。
func (s *Server) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if s.publicURL != "" {
		if p, err := url.Parse(s.publicURL); err == nil && strings.EqualFold(u.Host, p.Host) {
			return true
		}
	}
	return false
}

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// handleWS 完成握手后保持连接，推送事件直到页面关闭、会话失效或面板退出。
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !headerHas(r.Header, "Connection", "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.writeError(w, r, errorf(CodeBadRequest, "需要 WebSocket 连接"))
		return
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		s.writeError(w, r, errorf(CodeBadRequest, "不支持的 WebSocket 版本"))
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if k, err := base64.StdEncoding.DecodeString(key); err != nil || len(k) != 16 {
		s.writeError(w, r, errorf(CodeBadRequest, "WebSocket 握手不正确"))
		return
	}
	if !s.sameOrigin(r) {
		s.writeError(w, r, errorf(CodeForbidden, ""))
		return
	}
	token := info(r).sessionToken
	c := s.ws.add()
	if c == nil {
		s.writeError(w, r, errorf(CodeUnavailable, "实时连接过多，请稍后再试"))
		return
	}
	defer s.ws.remove(c)

	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		s.log.Warn("websocket hijack failed", "err", err)
		return
	}
	defer conn.Close()
	info(r).hijacked = true
	sum := sha1.Sum([]byte(key + wsGUID))
	conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if _, err := brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"); err != nil {
		return
	}
	if err := brw.Flush(); err != nil {
		return
	}

	// 读：处理 ping / 关闭，其余丢弃；任何错误都结束连接
	pongs := make(chan []byte, 1)
	go func() {
		defer c.close()
		for {
			conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
			op, payload, err := wsReadFrame(brw.Reader)
			if err != nil {
				return
			}
			switch op {
			case 0x8: // 关闭
				return
			case 0x9: // ping → pong
				select {
				case pongs <- payload:
				default:
				}
			}
		}
	}()

	// 写：事件、pong、定时 ping 与会话校验都在这一个 goroutine 中，保证帧不交错
	tick := time.NewTicker(wsPingInterval)
	defer tick.Stop()
	write := func(op byte, payload []byte) error {
		conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if err := wsWriteFrame(brw.Writer, op, payload); err != nil {
			return err
		}
		return brw.Flush()
	}
	for {
		select {
		case <-c.done:
			write(0x8, wsClosePayload(1000, ""))
			return
		case b := <-c.send:
			if write(0x1, b) != nil {
				return
			}
		case p := <-pongs:
			if write(0xA, p) != nil {
				return
			}
		case <-tick.C:
			// 【安全】会话已退出、被踢出或过期（或无法确认）：关闭连接（1008 违反策略），页面重连时按 401 回到登录页
			if se, err := s.store.LookupSession(token, time.Now()); err != nil || se == nil {
				write(0x8, wsClosePayload(1008, "session expired"))
				return
			}
			if write(0x9, nil) != nil {
				return
			}
		}
	}
}

func wsClosePayload(code uint16, reason string) []byte {
	b := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(b, code)
	return append(b, reason...)
}

// wsWriteFrame 写一个不分片、不加掩码的帧（服务端发出的帧不加掩码，RFC 6455 5.1）。
func wsWriteFrame(w *bufio.Writer, op byte, payload []byte) error {
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// wsPingInterval 是 ping 与会话重新校验的间隔；测试中缩短。
var wsPingInterval = 30 * time.Second

var errWSFrame = errors.New("websocket: invalid frame")

// wsReadFrame 读取一个客户端帧并去掉掩码。客户端帧必须加掩码，长度不超过 wsMaxFrame（RFC 6455 5.1、5.2）。
func wsReadFrame(r *bufio.Reader) (op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	op = h[0] & 0x0F
	if h[1]&0x80 == 0 {
		return 0, nil, errWSFrame
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > wsMaxFrame {
		return 0, nil, errWSFrame
	}
	var mask [4]byte
	if _, err = io.ReadFull(r, mask[:]); err != nil {
		return
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return op, payload, nil
}
