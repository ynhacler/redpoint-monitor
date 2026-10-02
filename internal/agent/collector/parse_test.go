package collector

import "testing"

func TestParseNetDev(t *testing.T) {
	s := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0: 833721124344 1234 0 0 0 0 0 0 139922812321 4321 0 0 0 0 0 0
`
	m := parseNetDev(s)
	if m["eth0"].rx != 833721124344 || m["eth0"].tx != 139922812321 {
		t.Fatalf("eth0 = %+v", m["eth0"])
	}
	if _, ok := m["lo"]; !ok {
		t.Fatal("lo missing")
	}
}

func TestCPUUsage(t *testing.T) {
	a, cores := parseProcStat("cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 1 1 1\ncpu1 1 1 1 1\n")
	b, _ := parseProcStat("cpu  150 0 150 900 0 0 0 0 0 0\n")
	if cores != 2 {
		t.Fatalf("cores = %d", cores)
	}
	if got := cpuUsage(a, b); got != 50 {
		t.Fatalf("usage = %v, want 50", got)
	}
}

func TestDefaultRoute(t *testing.T) {
	s := "Iface\tDestination\tGateway\nens3\t00000000\t0101A8C0\nens3\t0001A8C0\t00000000\n"
	if got := parseDefaultRouteIfaces(s); len(got) != 1 || got[0] != "ens3" {
		t.Fatalf("got %v", got)
	}
}

func TestExcluded(t *testing.T) {
	for _, n := range []string{"lo", "docker0", "veth12ab", "wg0", "br-abc"} {
		if !excluded(n, DefaultExclude) {
			t.Errorf("%s should be excluded", n)
		}
	}
	if excluded("eth0", DefaultExclude) {
		t.Error("eth0 should not be excluded")
	}
}
