package diskusage

import "testing"

func TestOfTheTempDir(t *testing.T) {
	u, err := Of(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if u.Total == 0 || u.Free > u.Total {
		t.Fatalf("usage: %+v", u)
	}
	if p := u.UsedPercent(); p < 0 || p > 100 {
		t.Fatalf("used %%: %v", p)
	}
	if (Usage{}).UsedPercent() != 0 {
		t.Fatal("empty usage")
	}
}
