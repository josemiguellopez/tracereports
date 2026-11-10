package db

import (
	"errors"
	"testing"
)

// Cómo llegó la evidencia de un run: se guarda, se lee con el run y aparece en la lista de
// Mantenimiento > Conexiones solo si hubo algo que reportar (y dentro del período pedido).
func TestRunDeliveryStoredAndListed(t *testing.T) {
	s := openTestStore(t)
	late, _ := s.CreateRunWithMeta("clinico/test_a.py", "qa", RunMeta{ExecutionID: "suite-1", Project: "clinico"})
	ok, _ := s.CreateRun("sin problemas", "qa")
	if d, err := s.RunDeliveryOf(ok); err != nil || d != nil {
		t.Fatalf("a run without report: %v %v", d, err)
	}
	if (RunDelivery{Client: "python 0.5.2"}).Notable() {
		t.Fatal("a report without problems is not notable")
	}
	want := RunDelivery{Late: true, LateAt: 1700, Reason: "the server did not answer within 30 s", Detail: "connected to 10.0.0.25:8080 in 3 ms, no answer in 10 s", Retried: 2, Client: "python 0.5.2", Host: "ntbk"}
	if !want.Notable() {
		t.Fatal("late is notable")
	}
	if err := s.SetRunDelivery(late, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.RunDeliveryOf(late)
	if err != nil || got == nil || *got != want {
		t.Fatalf("read back %+v %v", got, err)
	}
	list, err := s.DeliveryProblems(0, 10)
	if err != nil || len(list) != 1 || list[0].ID != late || list[0].ExecutionID != "suite-1" || list[0].Project != "clinico" || !list[0].Delivery.Late {
		t.Fatalf("problems %+v %v", list, err)
	}
	if list, _ := s.DeliveryProblems(1<<62, 10); len(list) != 0 {
		t.Fatal("outside the period")
	}
	if err := s.SetRunDelivery(999, want); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
}
