package mathx

import "testing"

func TestIntHelpers(t *testing.T) {
	if MinInt(2, 1) != 1 {
		t.Fatalf("MinInt failed")
	}
	if MaxInt(2, 1) != 2 {
		t.Fatalf("MaxInt failed")
	}
	if AbsInt(-3) != 3 {
		t.Fatalf("AbsInt failed")
	}
}
