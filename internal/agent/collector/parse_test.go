package collector

import (
	"strings"
	"testing"
)

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

func TestSelectMounts(t *testing.T) {
	s := `/dev/vda1 / ext4 rw,relatime 0 0
proc /proc proc rw 0 0
tmpfs /run tmpfs rw 0 0
/dev/vda15 /boot/efi vfat rw 0 0
/dev/vdb1 /data xfs rw 0 0
/dev/vdb1 /var/lib/docker/volumes/x xfs rw 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw 0 0
/dev/loop0 /snap/core/1 squashfs ro 0 0
nas:/export /mnt/nas nfs4 rw 0 0
/dev/vdc1 /mnt/my\040disk ext4 rw 0 0
`
	got := selectMounts(parseMounts(s))
	var mounts []string
	for _, m := range got {
		mounts = append(mounts, m.Mount)
	}
	want := "/,/boot/efi,/data,/mnt/my disk"
	if strings.Join(mounts, ",") != want {
		t.Errorf("挂载点 = %q，应为 %q（排除虚拟 / 网络文件系统，同一设备只取一次，/ 在最前，还原 \\040）",
			strings.Join(mounts, ","), want)
	}
}

func TestParseDiskstats(t *testing.T) {
	s := ` 253       0 vda 1000 10 20000 500 2000 20 40000 900 0 1200 1400 0 0 0 0
 253       1 vda1 900 10 18000 450 1900 20 39000 880 0 1100 1330 0 0 0 0
   7       0 loop0 5 0 10 0 0 0 0 0 0 4 0 0 0 0 0
`
	m := parseDiskstats(s)
	v := m["vda"]
	if v.readOps != 1000 || v.readBytes != 20000*512 || v.writeOps != 2000 || v.writeBytes != 40000*512 || v.ioTimeMs != 1200 {
		t.Errorf("vda = %+v", v)
	}
	if !skipIODevice("loop0") || !skipIODevice("zram0") || skipIODevice("nvme0n1") || skipIODevice("vda") {
		t.Error("skipIODevice 判断错误")
	}
}
