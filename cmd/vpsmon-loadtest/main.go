// vpsmon-loadtest 模拟大量 Agent 同时上报，测量面板的承载能力（开发工具，不随版本发布）。
//
// 三个阶段：
//
//	steady  每个模拟 Agent 按 -interval 上报（gzip 压缩、保持连接，与真实 Agent 相同），同时每 3 秒请求一次节点列表（模拟打开的 Web 页面）
//	burst   面板重启后的补发高峰：每个 Agent 立即连续补发 -backlog 份积压的上报
//	report  输出各阶段的延迟分布、吞吐与错误数；-pid 指定时采样面板进程的 CPU 与常驻内存
//
// 用法见 scripts/loadtest.sh。
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"vpsmon/internal/protocol"
)

func main() {
	server := flag.String("server", "http://127.0.0.1:18090", "panel base URL")
	tokensFile := flag.String("tokens", "", "file with one agent token per line")
	interval := flag.Duration("interval", 10*time.Second, "report interval per simulated agent")
	duration := flag.Duration("duration", 60*time.Second, "steady phase duration")
	backlog := flag.Int("backlog", 180, "reports per agent in the burst phase (0 = skip)")
	user := flag.String("user", "admin", "admin username for the list probe")
	pass := flag.String("pass", os.Getenv("LOADTEST_PASSWORD"), "admin password for the list probe (empty = skip)")
	pid := flag.Int("pid", 0, "panel process ID to sample CPU / RSS (0 = skip)")
	flag.Parse()

	b, err := os.ReadFile(*tokensFile)
	if err != nil {
		log.Fatal(err)
	}
	tokens := strings.Fields(string(b))
	if len(tokens) == 0 {
		log.Fatal("no tokens")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: len(tokens) + 4}}
	endpoint := strings.TrimRight(*server, "/") + "/api/v1/agent/report"
	agents := make([]*agent, len(tokens))
	for i, t := range tokens {
		agents[i] = newAgent(i, t)
	}

	var sampler *procSampler
	if *pid > 0 {
		sampler = startSampler(*pid)
	}

	// steady
	fmt.Printf("== steady: %d agents, interval %s, %s ==\n", len(agents), *interval, *duration)
	steady := &stats{}
	listStats := &stats{}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, a := range agents {
		wg.Add(1)
		go func(a *agent) {
			defer wg.Done()
			time.Sleep(time.Duration(rand.Int63n(int64(*interval)))) // 错开起始时间，与真实部署一致
			t := time.NewTicker(*interval)
			defer t.Stop()
			for {
				a.post(client, endpoint, time.Now(), steady)
				select {
				case <-stop:
					return
				case <-t.C:
				}
			}
		}(a)
	}
	if *pass != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeList(*server, *user, *pass, stop, listStats)
		}()
	}
	time.Sleep(*duration)
	close(stop)
	wg.Wait()
	steady.print("report POST", *duration)
	listStats.print("GET /servers", *duration)
	if sampler != nil {
		sampler.print("panel (steady)")
		sampler.reset()
	}

	// burst
	if *backlog > 0 {
		fmt.Printf("== burst: %d agents × %d backlog reports ==\n", len(agents), *backlog)
		burst := &stats{}
		start := time.Now()
		var bw sync.WaitGroup
		for _, a := range agents {
			bw.Add(1)
			go func(a *agent) {
				defer bw.Done()
				for i := *backlog; i > 0; i-- {
					a.post(client, endpoint, start.Add(-time.Duration(i)*10*time.Second), burst)
				}
			}(a)
		}
		bw.Wait()
		took := time.Since(start)
		burst.print("report POST", took)
		if sampler != nil {
			sampler.print("panel (burst)")
		}
	}
}

// agent 是一个模拟的 Agent：网卡计数单调递增，上报大小与真实 Agent 相近（约 2 KB，压缩后约 1 KB）。
type agent struct {
	id     int
	token  string
	rx, tx uint64
	mu     sync.Mutex
	gz     *gzip.Writer
	buf    bytes.Buffer
}

func newAgent(id int, token string) *agent {
	a := &agent{id: id, token: token, rx: uint64(rand.Int63n(1 << 40)), tx: uint64(rand.Int63n(1 << 38))}
	a.gz, _ = gzip.NewWriterLevel(&a.buf, gzip.BestSpeed)
	return a
}

func (a *agent) report(at time.Time) protocol.Report {
	a.rx += uint64(rand.Int63n(50 << 20))
	a.tx += uint64(rand.Int63n(10 << 20))
	r := protocol.Report{Timestamp: at.Unix(), SentAt: time.Now().Unix(), AgentVersion: "loadtest"}
	r.System = protocol.System{Hostname: fmt.Sprintf("load-%03d", a.id), OS: "debian", OSVersion: "13", Kernel: "6.12.0",
		Arch: "amd64", Uptime: 86400, BootID: fmt.Sprintf("boot-%03d", a.id), CPUModel: "AMD EPYC 7B13", CounterBits: 64}
	r.CPU = protocol.CPU{Usage: rand.Float64() * 100, Cores: 4, Load1: rand.Float64() * 4,
		Breakdown: &protocol.CPUBreakdown{User: 10, System: 3, IOWait: 1, Steal: rand.Float64() * 5, Idle: 80},
		PerCore:   []float64{20, 15, 18, 19}}
	r.Memory = protocol.Memory{Total: 4 << 30, Used: uint64(rand.Int63n(4 << 30)), Usage: rand.Float64() * 100}
	r.Swap = protocol.Swap{Total: 1 << 30, Used: 1 << 27}
	r.Disk = []protocol.Disk{{Mount: "/", Total: 40 << 30, Used: 18 << 30, Usage: 45, FSType: "ext4", Device: "/dev/vda1"}}
	r.Network = []protocol.NetIface{{Interface: "eth0", IfIndex: 2, RxBytes: a.rx, TxBytes: a.tx, RxSpeed: 1 << 20, TxSpeed: 1 << 18}}
	r.DiskIO = []protocol.DiskIO{{Device: "vda", ReadBytes: 1 << 34, WriteBytes: 1 << 35, ReadSpeed: 1 << 12, WriteSpeed: 1 << 16}}
	r.Processes = &protocol.Processes{Total: 140, Running: 1}
	r.Conns = &protocol.Conns{TCP: 37, UDP: 6, TimeWait: 12}
	for _, p := range []int{22, 80, 443, 3306, 8080} {
		r.Ports = append(r.Ports, protocol.ListenPort{Proto: "tcp", Port: p, Addrs: []string{"0.0.0.0", "::"}})
	}
	return r
}

func (a *agent) post(c *http.Client, endpoint string, at time.Time, st *stats) {
	a.mu.Lock()
	body, _ := json.Marshal(a.report(at))
	a.buf.Reset()
	a.gz.Reset(&a.buf)
	a.gz.Write(body)
	a.gz.Close()
	payload := slices.Clone(a.buf.Bytes())
	a.mu.Unlock()

	req, _ := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	start := time.Now()
	resp, err := c.Do(req)
	d := time.Since(start)
	if err != nil {
		st.add(d, false)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	st.add(d, resp.StatusCode/100 == 2)
}

// probeList 登录后每 3 秒请求一次节点列表，模拟一个打开着的 Web 页面。
func probeList(server, user, pass string, stop chan struct{}, st *stats) {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	body, _ := json.Marshal(map[string]any{"username": user, "password": pass})
	resp, err := c.Post(server+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		log.Printf("list probe: login failed: %v %v", err, resp)
		return
	}
	resp.Body.Close()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		start := time.Now()
		resp, err := c.Get(server + "/api/v1/servers")
		d := time.Since(start)
		ok := err == nil && resp.StatusCode == 200
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		st.add(d, ok)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

type stats struct {
	mu   sync.Mutex
	lat  []time.Duration
	errs atomic.Int64
}

func (s *stats) add(d time.Duration, ok bool) {
	if !ok {
		s.errs.Add(1)
	}
	s.mu.Lock()
	s.lat = append(s.lat, d)
	s.mu.Unlock()
}

func (s *stats) print(name string, over time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lat) == 0 {
		return
	}
	slices.Sort(s.lat)
	p := func(q float64) time.Duration {
		return s.lat[min(len(s.lat)-1, int(q*float64(len(s.lat))))].Round(10 * time.Microsecond)
	}
	fmt.Printf("%-14s n=%-6d %6.1f req/s  p50 %-9s p95 %-9s p99 %-9s max %-9s errors %d\n", name, len(s.lat),
		float64(len(s.lat))/over.Seconds(), p(0.5), p(0.95), p(0.99), s.lat[len(s.lat)-1].Round(10*time.Microsecond), s.errs.Load())
}

// procSampler 每秒用 ps 采样一次面板进程的 CPU 与常驻内存（macOS / Linux 通用）。
type procSampler struct {
	mu       sync.Mutex
	cpu, rss []float64
	pid      int
}

func startSampler(pid int) *procSampler {
	s := &procSampler{pid: pid}
	go func() {
		for range time.Tick(time.Second) {
			out, err := exec.Command("ps", "-o", "%cpu=,rss=", "-p", strconv.Itoa(pid)).Output()
			if err != nil {
				return
			}
			f := strings.Fields(string(out))
			if len(f) < 2 {
				continue
			}
			c, _ := strconv.ParseFloat(f[0], 64)
			r, _ := strconv.ParseFloat(f[1], 64)
			s.mu.Lock()
			s.cpu, s.rss = append(s.cpu, c), append(s.rss, r)
			s.mu.Unlock()
		}
	}()
	return s
}

func (s *procSampler) reset() {
	s.mu.Lock()
	s.cpu, s.rss = nil, nil
	s.mu.Unlock()
}

func (s *procSampler) print(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cpu) == 0 {
		return
	}
	avg := func(v []float64) float64 {
		t := 0.0
		for _, x := range v {
			t += x
		}
		return t / float64(len(v))
	}
	fmt.Printf("%-14s CPU avg %.1f%% max %.1f%%  RSS avg %.1f MB max %.1f MB\n", name,
		avg(s.cpu), slices.Max(s.cpu), avg(s.rss)/1024, slices.Max(s.rss)/1024)
}
