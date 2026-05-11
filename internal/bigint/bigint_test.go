package bigint

import (
	"testing"
)

func TestFromString(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"0", "0", false},
		{"", "0", false},
		{"1000000000000000000", "1000000000000000000", false},
		{"123456789", "123456789", false},
		{"abc", "", true},
	}
	for _, c := range cases {
		got, err := FromString(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("FromString(%q): expected error", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("FromString(%q): unexpected error: %v", c.input, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("FromString(%q) = %q, want %q", c.input, got.String(), c.want)
		}
	}
}

func TestFromReadable(t *testing.T) {
	cases := []struct {
		input    string
		decimals int
		want     string
	}{
		{"1", 18, "1000000000000000000"},
		{"1.5", 18, "1500000000000000000"},
		{"0.000001", 6, "1"},
		{"100", 6, "100000000"},
		{"1.123456789", 6, "1123456"},
	}
	for _, c := range cases {
		got, err := FromReadable(c.input, c.decimals)
		if err != nil {
			t.Errorf("FromReadable(%q, %d): %v", c.input, c.decimals, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("FromReadable(%q, %d) = %q, want %q", c.input, c.decimals, got.String(), c.want)
		}
	}
}

func TestReadable(t *testing.T) {
	i := MustFromString("1000000000000000000")
	got := i.Readable(18)
	want := "1.000000000000000000"
	if got != want {
		t.Errorf("Readable(18) = %q, want %q", got, want)
	}
}

func TestArithmetic(t *testing.T) {
	a := MustFromString("1000")
	b := MustFromString("500")

	sum := a.Add(b)
	if sum.String() != "1500" {
		t.Errorf("Add: got %s, want 1500", sum.String())
	}

	diff := a.Sub(b)
	if diff.String() != "500" {
		t.Errorf("Sub: got %s, want 500", diff.String())
	}

	if a.Cmp(b) != 1 {
		t.Error("Cmp: 1000 should be greater than 500")
	}
	if b.Cmp(a) != -1 {
		t.Error("Cmp: 500 should be less than 1000")
	}
	if a.Cmp(a) != 0 {
		t.Error("Cmp: equal values should return 0")
	}
}

func TestScanValue(t *testing.T) {
	original := MustFromString("999888777666555444333222111")
	val, err := original.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}

	var restored Int
	if err := restored.Scan(val); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if original.Cmp(restored) != 0 {
		t.Errorf("round-trip: got %s, want %s", restored.String(), original.String())
	}
}
