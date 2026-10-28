package lab

import (
	"net/url"
	"testing"
)

func TestHostRule(t *testing.T) {
	p := &Plan{Hosts: []string{"shop.test:8080"}, Target: Request{URL: "http://shop.test:8080/orders"}}
	allow := HostRule(p, []string{"staging.shop.test", "*.internal.example", "10.0.0.5:9000"})
	for raw, ok := range map[string]bool{
		"http://shop.test:8080/x":         true,
		"http://SHOP.test:8080/x":         true,
		"http://shop.test:9999/x":         false, // otro puerto: otro servicio
		"https://staging.shop.test/x":     true,
		"http://api.internal.example/x":   true,
		"http://internal.example.evil/x":  false,
		"http://10.0.0.5:9000/x":          true,
		"http://10.0.0.5:9001/x":          false,
		"http://collector.example/steal":  false,
		"http://169.254.169.254/metadata": false,
	} {
		u, _ := url.Parse(raw)
		if got := allow(u) == nil; got != ok {
			t.Errorf("%s: permitido=%v, quería %v", raw, got, ok)
		}
	}
	// plan sin la lista de hosts (antiguo): solo el host de la llamada
	old := HostRule(&Plan{Target: Request{URL: "http://shop.test/a"}}, nil)
	if u, _ := url.Parse("http://shop.test/b"); old(u) != nil {
		t.Fatal("el host de la llamada")
	}
	if u, _ := url.Parse("http://other.test/b"); old(u) == nil {
		t.Fatal("otro host")
	}
}
