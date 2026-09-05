package geoip

import "testing"

func TestLookupKnownRanges(t *testing.T) {
	// 1.1.1.0/24 -> AU, 8.8.8.8 -> US, 4.0.0.0/8 -> US (dbip lite head ranges).
	cases := map[string]string{
		"1.1.1.1":  "AU",
		"8.8.8.8":  "US",
		"1.32.0.1": "MY",
		"16777216": "", // numeric strings are not accepted; only dotted quad
	}
	for ip, want := range cases {
		if got := Lookup(ip); got != want {
			t.Errorf("Lookup(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestLookupUnknownAndInvalid(t *testing.T) {
	for _, ip := range []string{"", "not-an-ip", "2001:db8::1", "999.1.1.1", "0.0.0.0"} {
		if got := Lookup(ip); got != "" {
			t.Errorf("Lookup(%q) = %q, want empty", ip, got)
		}
	}
}

func TestEnabled(t *testing.T) {
	if !Enabled() {
		t.Fatal("embedded database should be present")
	}
}
