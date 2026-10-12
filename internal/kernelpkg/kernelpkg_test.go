package kernelpkg

import (
	"reflect"
	"testing"
)

func TestParseDeb(t *testing.T) {
	ok := map[string]string{
		"linux-image-7.0.0-28-generic":          "7.0.0-28",
		"linux-image-unsigned-6.8.0-45-generic": "6.8.0-45",
		"linux-headers-7.0.0-28":                "7.0.0-28",
		"linux-headers-7.0.0-28-generic":        "7.0.0-28",
		"linux-modules-7.0.0-28-generic":        "7.0.0-28",
		"linux-modules-extra-6.8.0-45-generic":  "6.8.0-45",
		"linux-tools-6.8.0-45-generic":          "6.8.0-45",
		"linux-cloud-tools-6.8.0-45-generic":    "6.8.0-45",
		"linux-buildinfo-6.8.0-45-generic":      "6.8.0-45",
		"linux-image-6.1.0-18-amd64":            "6.1.0-18",
		"linux-image-6.1.0-18-cloud-amd64":      "6.1.0-18",
		"linux-headers-6.1.0-18-common":         "6.1.0-18",
		"linux-image-6.5.0-1020-aws":            "6.5.0-1020",
	}
	for n, want := range ok {
		if got, o := ParseDeb(n); !o || got != want {
			t.Errorf("%s: got %q %v want %q", n, got, o, want)
		}
	}
	for _, n := range []string{"linux-image-generic", "linux-generic", "linux-image-virtual", "linux-libc-dev", "linux-firmware",
		"linux-headers-generic", "linux-tools-common", "linux-base", "kernel", "linux-image-7.0.0", "xlinux-image-7.0.0-28-generic", "linux-image-7.0.0-28-"} {
		if _, o := ParseDeb(n); o {
			t.Errorf("%s should not parse", n)
		}
	}
}

func TestParseRPM(t *testing.T) {
	for _, n := range []string{"kernel", "kernel-core", "kernel-modules", "kernel-modules-core", "kernel-modules-extra", "kernel-devel"} {
		if abi, ok := ParseRPM(n, "5.14.0-427.13.1.el9_4"); !ok || abi != "5.14.0-427.13.1.el9_4" {
			t.Errorf("%s: %q %v", n, abi, ok)
		}
	}
	if abi, _ := ParseRPM("kernel", "1:5.14.0-1.el9"); abi != "5.14.0-1.el9" {
		t.Errorf("epoch not stripped: %q", abi)
	}
	for _, n := range []string{"kernel-headers", "kernel-tools", "kernel-srpm-macros", "bash"} {
		if _, ok := ParseRPM(n, "1.0-1"); ok {
			t.Errorf("%s should not parse", n)
		}
	}
	if _, ok := Parse("deb", "linux-image-7.0.0-28-generic", ""); !ok {
		t.Error("Parse deb")
	}
	if _, ok := Parse("rpm", "kernel-core", "5.1-1"); !ok {
		t.Error("Parse rpm")
	}
	if _, ok := Parse("apk", "linux-lts", "5.1-1"); ok {
		t.Error("Parse apk")
	}
}

func TestMatchesRunning(t *testing.T) {
	cases := []struct {
		abi, run string
		want     bool
	}{
		{"7.0.0-38", "7.0.0-38-generic", true},
		{"7.0.0-28", "7.0.0-38-generic", false},
		{"7.0.0-3", "7.0.0-38-generic", false},
		{"6.1.0-18", "6.1.0-18-amd64", true},
		{"5.14.0-427.13.1.el9_4", "5.14.0-427.13.1.el9_4.x86_64", true},
		{"5.14.0-427.1", "5.14.0-427.13.1.el9_4.x86_64", false},
		{"7.0.0-38", "7.0.0-38", true},
		{"", "7.0.0-38-generic", false},
		{"7.0.0-38", "", false},
	}
	for _, c := range cases {
		if got := MatchesRunning(c.abi, c.run); got != c.want {
			t.Errorf("%s vs %s: %v", c.abi, c.run, got)
		}
	}
}

func TestCompareNewest(t *testing.T) {
	if Compare("7.0.0-28", "7.0.0-38") >= 0 || Compare("7.0.0-38", "7.0.0-28") <= 0 || Compare("7.0.0-38", "7.0.0-38") != 0 {
		t.Error("basic")
	}
	if Compare("7.0.0-9", "7.0.0-10") >= 0 {
		t.Error("numeric not lexical")
	}
	if Compare("6.8.0-45", "7.0.0-1") >= 0 {
		t.Error("major")
	}
	if Compare("5.14.0-427.13.1.el9_4", "5.14.0-503.11.1.el9_5") >= 0 {
		t.Error("rpm")
	}
	if Compare("5.14.0-427.9.el9", "5.14.0-427.13.1.el9") >= 0 {
		t.Error("rpm minor")
	}
	if Compare("6.1.0-18", "6.1.0-18.1") >= 0 {
		t.Error("longer wins")
	}
	if Newest([]string{"7.0.0-28", "7.0.0-38", "6.8.0-45"}) != "7.0.0-38" || Newest(nil) != "" {
		t.Error("newest")
	}
}

func TestRemovable(t *testing.T) {
	got := Removable([]string{"7.0.0-28", "7.0.0-38", "7.0.0-28", "6.8.0-45"}, "7.0.0-38-generic")
	if !reflect.DeepEqual(got, []string{"6.8.0-45", "7.0.0-28"}) {
		t.Errorf("got %v", got)
	}
	// Running is older than the newest: keep both.
	got = Removable([]string{"7.0.0-28", "7.0.0-38", "6.8.0-45"}, "7.0.0-28-generic")
	if !reflect.DeepEqual(got, []string{"6.8.0-45"}) {
		t.Errorf("got %v", got)
	}
	if got := Removable([]string{"7.0.0-38"}, "7.0.0-38-generic"); len(got) != 0 {
		t.Errorf("got %v", got)
	}
	// Unknown running kernel still keeps the newest.
	if got := Removable([]string{"7.0.0-28", "7.0.0-38"}, ""); !reflect.DeepEqual(got, []string{"7.0.0-28"}) {
		t.Errorf("got %v", got)
	}
}

func TestValidABI(t *testing.T) {
	for _, s := range []string{"7.0.0-28", "6.1.0-18", "5.14.0-427.13.1.el9_4", "6.5.0-1020"} {
		if !ValidABI(s) {
			t.Errorf("%s rejected", s)
		}
	}
	for _, s := range []string{"", "7.0.0", "7.0.0-28; rm -rf /", "-7.0.0-28", "7.0.0-28 ", "$(id)", "7.0.0-28\n", "../7.0.0-28", "7.0.0-28-generic"} {
		if ValidABI(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestIsMeta(t *testing.T) {
	if !IsMeta("linux-generic") || !IsMeta("linux-image-virtual") || IsMeta("linux-image-7.0.0-28-generic") {
		t.Error("meta")
	}
}
