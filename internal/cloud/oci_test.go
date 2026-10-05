package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeOCI struct {
	mu     sync.Mutex
	paths  []string
	bodies []map[string]any
	deny   bool
}

func (f *fakeOCI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), `Signature version="1",keyId="ocid1.tenancy.oc1..t/ocid1.user.oc1..u/aa:bb",algorithm="rsa-sha256"`) {
			t.Errorf("签名头不对：%s", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			json.Unmarshal(b, &body)
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path+"?"+r.URL.RawQuery)
		f.bodies = append(f.bodies, body)
		deny := f.deny
		f.mu.Unlock()
		if deny {
			w.WriteHeader(401)
			io.WriteString(w, `{"code":"NotAuthenticated","message":"The required information to complete authentication was not provided."}`)
			return
		}
		p := r.URL.Path
		comp := r.URL.Query().Get("compartmentId")
		switch {
		case strings.HasSuffix(p, "/regionSubscriptions"):
			io.WriteString(w, `[{"regionName":"ap-tokyo-1","status":"READY"},{"regionName":"us-ashburn-1","status":"READY"}]`)
		case strings.HasSuffix(p, "/compartments"):
			io.WriteString(w, `[{"id":"ocid1.compartment.oc1..child"}]`)
		case strings.HasSuffix(p, "/instances") && strings.Contains(p, "ap-tokyo-1") && comp == "ocid1.compartment.oc1..child":
			if r.URL.Query().Get("page") == "" {
				w.Header().Set("opc-next-page", "p2")
				io.WriteString(w, `[{"id":"ocid1.instance.a","displayName":"tokyo-arm","shape":"VM.Standard.A1.Flex","lifecycleState":"RUNNING"}]`)
				return
			}
			io.WriteString(w, `[{"id":"ocid1.instance.b","displayName":"gone","lifecycleState":"TERMINATED"}]`)
		case strings.HasSuffix(p, "/instances"):
			io.WriteString(w, `[]`)
		case strings.HasSuffix(p, "/vnicAttachments"):
			io.WriteString(w, `[{"instanceId":"ocid1.instance.a","vnicId":"ocid1.vnic.a","lifecycleState":"ATTACHED"}]`)
		case strings.HasSuffix(p, "/vnics/ocid1.vnic.a"):
			io.WriteString(w, `{"publicIp":"152.69.0.1","ipv6Addresses":["2603:c021::1"]}`)
		case strings.HasSuffix(p, "/usage"):
			if body["queryType"] == "COST" {
				io.WriteString(w, `{"items":[{"computedAmount":1.234,"currency":"USD"},{"computedAmount":0.5,"currency":"USD"}]}`)
			} else {
				io.WriteString(w, `{"items":[{"skuName":"Outbound Data Transfer Zone 1","unit":"GB","computedQuantity":120.5},{"skuName":"Block Volume","computedQuantity":50}]}`)
			}
		default:
			t.Errorf("未预期的请求 %s", p)
			w.WriteHeader(404)
		}
	}
}

func newFakeOCI(t *testing.T, regions []string) (*fakeOCI, Client) {
	f := &fakeOCI{}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	cred, _ := json.Marshal(OCICredentials{TenancyOCID: "ocid1.tenancy.oc1..t", UserOCID: "ocid1.user.oc1..u", Fingerprint: "aa:bb",
		PrivateKey: ociTestKey, Region: "ap-tokyo-1"})
	c, err := NewClient(ProviderOCI, cred, regions, Options{Endpoint: func(service, region string) string {
		return srv.URL + "/" + service + "/" + region + "/"
	}})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestOCICosts(t *testing.T) {
	f, c := newFakeOCI(t, nil)
	got, err := c.Costs(context.Background(), time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.Period != "2026-10" || got.AmountCents != 173 || got.Currency != "USD" || got.BalanceCents != nil {
		t.Fatalf("费用 %+v", got)
	}
	b := f.bodies[len(f.bodies)-1]
	if b["tenantId"] != "ocid1.tenancy.oc1..t" || b["timeUsageStarted"] != "2026-10-01T00:00:00Z" || b["timeUsageEnded"] != "2026-11-01T00:00:00Z" ||
		b["granularity"] != "MONTHLY" {
		t.Fatalf("用量请求 %v", b)
	}
	if !strings.Contains(f.paths[len(f.paths)-1], "/usageapi/ap-tokyo-1/") {
		t.Fatalf("费用应在主区域查询：%v", f.paths)
	}
}

func TestOCIInstancesAndEgress(t *testing.T) {
	f, c := newFakeOCI(t, []string{"ap-tokyo-1"})
	insts, err := c.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 2 {
		t.Fatalf("实例 %+v", insts)
	}
	a, eg := insts[0], insts[1]
	if a.ID != "ocid1.instance.a" || a.Name != "tokyo-arm" || a.IPv4 != "152.69.0.1" || a.IPv6 != "2603:c021::1" ||
		a.Plan != "VM.Standard.A1.Flex" || a.Region != "ap-tokyo-1" || a.Kind != "oci" {
		t.Fatalf("实例 %+v", a)
	}
	if eg.Kind != "oci_egress" || eg.TrafficLimit != ociFreeEgress {
		t.Fatalf("出站流量项 %+v", eg)
	}
	for _, p := range f.paths {
		if strings.Contains(p, "us-ashburn-1") {
			t.Fatal("只应查询账户设置的区域")
		}
	}
	if err := c.Traffic(context.Background(), insts, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if insts[1].TrafficUsed != 120_500_000_000 || insts[1].TrafficPeriodStart != "2026-10-01" || insts[0].TrafficUsed != 0 {
		t.Fatalf("出站流量 %+v", insts[1])
	}
}

func TestOCIAuthError(t *testing.T) {
	f, c := newFakeOCI(t, nil)
	f.deny = true
	_, err := c.Costs(context.Background(), time.Now())
	if !IsAuthError(err) || !strings.Contains(err.Error(), "NotAuthenticated") {
		t.Fatalf("凭证无效应识别为认证错误：%v", err)
	}
	if _, err := NewClient(ProviderOCI, []byte(`{"tenancy_ocid":"x","user_ocid":"y","fingerprint":"z","region":"r","private_key":"bad"}`), nil, Options{}); err == nil {
		t.Fatal("无效私钥应报错")
	}
}
