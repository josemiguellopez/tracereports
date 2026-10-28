package similar

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestTermsNormalizeIDsAndNumbers(t *testing.T) {
	got := Terms("backend:POST shop.test/api/orders/:id HTTP 500 | AssertionError|expected # got 3|tests/test_a.py:42 deadbeefcafe1234")
	want := []string{"backend", "post", "shop", "test", "api", "orders", "id", "http", "500", "assertionerror", "expected", "got", "#", "tests", "test_a", "py", "#", "#"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestRankIdenticalDifferentAndThreshold(t *testing.T) {
	q := Terms("backend:POST shop.test/api/orders HTTP 500 BACKEND_TIMEOUT backend_5xx")
	docs := []Doc{
		{ID: 1, Terms: Terms("backend:POST shop.test/api/orders HTTP 500 BACKEND_TIMEOUT backend_5xx"), CreatedAt: 1},
		{ID: 2, Terms: Terms("backend:POST shop.test/api/orders HTTP 503 BACKEND_TIMEOUT backend_5xx"), CreatedAt: 2},
		{ID: 3, Terms: Terms("error:AssertionError|expected # got #|tests/test_cart.py:12 LOGIC_BUG assertion"), CreatedAt: 3},
		{ID: 4, Terms: Terms("error:TimeoutError|waiting for locator #pay LOCATOR_CHANGED locator_not_found"), CreatedAt: 4},
	}
	got := Rank(q, docs, 3, 0.6)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Score < 0.999 {
		t.Fatalf("idéntico ≈ 1.0: %v", got[0].Score)
	}
	// los distintos quedan bajo el umbral
	for _, m := range Rank(q, docs, 10, 0) {
		if (m.ID == 3 || m.ID == 4) && m.Score >= 0.6 {
			t.Fatalf("distinto sobre el umbral: %+v", m)
		}
	}
	if Rank(nil, docs, 3, 0) != nil || Rank(q, nil, 3, 0) != nil || Rank(q, docs, 0, 0) != nil {
		t.Fatal("entradas vacías")
	}
}

// A igual parecido gana el más reciente y luego el id: el orden no cambia entre llamadas.
func TestRankIsStable(t *testing.T) {
	terms := Terms("error:AssertionError|expected # got # LOGIC_BUG assertion")
	docs := []Doc{{ID: 7, Terms: terms, CreatedAt: 10}, {ID: 9, Terms: terms, CreatedAt: 20}, {ID: 8, Terms: terms, CreatedAt: 20}}
	want := []int64{9, 8, 7}
	for i := 0; i < 20; i++ {
		var ids []int64
		for _, m := range Rank(terms, docs, 3, 0.5) {
			ids = append(ids, m.ID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("vuelta %d: %v", i, ids)
		}
	}
}

func corpus(n int) []Doc {
	docs := make([]Doc, n)
	for i := range docs {
		docs[i] = Doc{ID: int64(i), CreatedAt: int64(i), Terms: Terms(fmt.Sprintf(
			"backend:POST svc%d.test/api/res%d/:id HTTP %d error:E%d|message id%d|pkg/file%d.go:%d CAT%d rule%d",
			i%50, i%300, 500+i%4, i%40, i, i%120, i, i%4, i%15))}
	}
	return docs
}

// Con 10 000 fallos clasificados la consulta sigue acotada (muy por debajo de una petición web).
func TestRankLatencyWith10kVerdicts(t *testing.T) {
	docs := corpus(10_000)
	q := docs[1234].Terms
	start := time.Now()
	got := Rank(q, docs, 3, 0.6)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("10 000 veredictos: %v", took)
	}
	if len(got) == 0 || got[0].ID != 1234 {
		t.Fatalf("%+v", got)
	}
}

func BenchmarkRank10k(b *testing.B) {
	docs := corpus(10_000)
	q := docs[1234].Terms
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Rank(q, docs, 3, 0.6)
	}
}
