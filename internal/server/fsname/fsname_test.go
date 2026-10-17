package fsname

import "testing"

func TestClean(t *testing.T) {
	ok := map[string]string{"": "", "/": "", "a/b/": "a/b", `a\b`: "a/b", "./a//b": "a/b"}
	for in, want := range ok {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "a/../..", "../x", "a/.homelydrive-trash/x", "a\x00b"} {
		if _, err := Clean(bad); err == nil {
			t.Errorf("Clean(%q) accepted", bad)
		}
	}
}

func TestNumbered(t *testing.T) {
	if got := Numbered("foto.jpg", 2); got != "foto (2).jpg" {
		t.Fatal(got)
	}
	if got := Numbered("Ordner", 1); got != "Ordner (1)" {
		t.Fatal(got)
	}
}

func TestRebaseWithin(t *testing.T) {
	if !IsWithin("a/b/c", "a/b") || IsWithin("a/bc", "a/b") || !IsWithin("x", "") {
		t.Fatal("IsWithin")
	}
	if Rebase("a/b/c", "a/b", "z") != "z/c" {
		t.Fatal("Rebase")
	}
}
