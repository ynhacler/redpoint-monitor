// vpsmon-server: single binary with embedded Web UI and SQLite.
//
//	vpsmon-server init        --data DIR               create DB, print a dev admin token
//	vpsmon-server add-server  --data DIR --name NAME   add a node, print its agent token
//	vpsmon-server run         --data DIR --listen ADDR
//
// Tokens are printed to stdout once and only their hashes are stored.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"vpsmon/internal/server"
	"vpsmon/web"
)

var version = "0.1.0-dev"

func usage() {
	fmt.Fprintf(os.Stderr, `vpsmon-server %s

usage:
  vpsmon-server init       --data DIR
  vpsmon-server add-server --data DIR --name NAME [--limit-gb N] [--reset-day D]
  vpsmon-server run        --data DIR [--listen 127.0.0.1:8080]
  vpsmon-server version
`, version)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	fsx := flag.NewFlagSet(cmd, flag.ExitOnError)
	data := fsx.String("data", "./data", "data directory")

	switch cmd {
	case "version":
		fmt.Println(version)

	case "init":
		_ = fsx.Parse(args)
		st := open(*data)
		tok, err := st.CreateAdminToken()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintln(os.Stderr, "initialised", *data, "— admin token (shown once):")
		fmt.Println(tok)

	case "add-server":
		name := fsx.String("name", "", "server name")
		limitGB := fsx.Int64("limit-gb", 0, "monthly traffic limit in GB (decimal), 0 = unlimited")
		resetDay := fsx.Int("reset-day", 1, "traffic reset day of month (1-31)")
		_ = fsx.Parse(args)
		if *name == "" {
			log.Fatal("--name is required")
		}
		st := open(*data)
		id, tok, err := st.CreateServer(*name, *limitGB*1_000_000_000, *resetDay)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "server %q created (id %d) — agent token (shown once):\n", *name, id)
		fmt.Println(tok)

	case "run":
		listen := fsx.String("listen", "127.0.0.1:8080", "listen address")
		_ = fsx.Parse(args)
		warnIfPublic(*listen)
		st := open(*data)
		srv, err := server.New(st, web.Dist())
		if err != nil {
			log.Fatal(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := srv.Run(ctx, *listen); err != nil {
			log.Fatal(err)
		}

	default:
		usage()
	}
}

func open(dir string) *server.Store {
	st, err := server.OpenStore(dir)
	if err != nil {
		log.Fatal(err)
	}
	return st
}

// Until Web login and built-in HTTPS exist (M3), keep the server on loopback and
// reach it through an SSH tunnel: ssh -L 8080:localhost:8080 your-vps
func warnIfPublic(listen string) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return
	}
	ip := net.ParseIP(host)
	if host == "localhost" || (ip != nil && ip.IsLoopback()) {
		return
	}
	log.Printf("WARNING: listening on %s without TLS. Development builds should stay on 127.0.0.1 behind an SSH tunnel.", listen)
}
