// vpsmon-agent: collects metrics and pushes them to vpsmon-server.
//
// Security invariants (docs/design.md 1.6.8, CLAUDE.md):
//   - only collect and report; never execute commands received from the server
//   - HTTPS only, except loopback or explicit --allow-http for local development
//   - runs as non-root
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"vpsmon/internal/agent/collector"
	"vpsmon/internal/protocol"
)

var version = "0.1.0-dev" // overridden via -ldflags "-X main.version=..."

func main() {
	server := flag.String("server", "", "server base URL, e.g. https://monitor.example.com")
	token := flag.String("token", "", "agent token (prefer --token-file or MONITOR_AGENT_TOKEN)")
	tokenFile := flag.String("token-file", "", "file containing the agent token")
	interval := flag.Duration("interval", 10*time.Second, "report interval")
	fake := flag.Bool("fake", false, "send fake metrics (for development)")
	allowHTTP := flag.Bool("allow-http", false, "allow plain HTTP to a non-loopback server (development only)")
	ifaces := flag.String("interfaces", "", "comma-separated interfaces to count; empty = auto")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	tok, err := loadToken(*token, *tokenFile)
	if err != nil {
		log.Fatal(err)
	}
	if err := checkServerURL(*server, *allowHTTP); err != nil {
		log.Fatal(err)
	}

	var col collector.Collector
	if *fake {
		col = collector.NewFake()
	} else {
		opts := collector.Options{}
		if *ifaces != "" {
			opts.Interfaces = strings.Split(*ifaces, ",")
		}
		col = collector.New(opts)
	}

	r := &reporter{
		endpoint: strings.TrimRight(*server, "/") + "/api/v1/agent/report",
		token:    tok,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
	log.Printf("vpsmon-agent %s → %s every %s", version, *server, *interval)

	// Prime CPU/network deltas so the first real report has speeds.
	_, _ = col.Collect()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	tick := time.NewTicker(*interval)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			r.send(context.Background(), col, false)
		case <-sig:
			// Final report so traffic between the last tick and shutdown is not lost (design 5.5).
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			r.send(ctx, col, true)
			cancel()
			log.Println("stopped")
			return
		}
	}
}

func loadToken(flagTok, file string) (string, error) {
	switch {
	case flagTok != "":
		return flagTok, nil
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	case os.Getenv("MONITOR_AGENT_TOKEN") != "":
		return os.Getenv("MONITOR_AGENT_TOKEN"), nil
	}
	return "", errors.New("no agent token: use --token-file or MONITOR_AGENT_TOKEN")
}

func checkServerURL(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid --server %q", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		h := u.Hostname()
		if h == "localhost" || h == "127.0.0.1" || h == "::1" || allowHTTP {
			return nil
		}
		return errors.New("refusing plain HTTP to a remote server; use HTTPS (or --allow-http for local dev)")
	}
	return fmt.Errorf("unsupported scheme %q", u.Scheme)
}

type reporter struct {
	endpoint string
	token    string
	client   *http.Client
}

func (r *reporter) send(ctx context.Context, col collector.Collector, final bool) {
	rep, err := col.Collect()
	if err != nil {
		log.Printf("collect: %v", err)
		return
	}
	rep.Timestamp = time.Now().Unix()
	rep.AgentVersion = version
	rep.Final = final
	if err := r.post(ctx, rep); err != nil {
		// TODO(M2): bounded in-memory retry buffer (design 1.6.14). Counters are cumulative,
		// so traffic is not lost on network outages, only metric points.
		log.Printf("report: %v", err)
	}
}

func (r *reporter) post(ctx context.Context, rep protocol.Report) error {
	body, _ := json.Marshal(rep)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}
