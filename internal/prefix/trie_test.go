package prefix

import "testing"

func TestTrieLongestMatch(t *testing.T) {
	tr := New[string]()
	tr.Insert("44", "UK")
	tr.Insert("447", "UK Mobile")
	tr.Insert("4477", "UK Mobile O2")
	tr.Insert("1", "USA")
	tr.Insert("", "default")

	cases := map[string]string{
		"447700900123": "UK Mobile O2",
		"447500900123": "UK Mobile",
		"442079460000": "UK",
		"12125551234":  "USA",
		"33123456789":  "default",
	}
	for num, want := range cases {
		got, _, ok := tr.Match(num)
		if !ok || got != want {
			t.Errorf("Match(%s) = %q,%v want %q", num, got, ok, want)
		}
	}
	got, matched, _ := tr.Match("447700900123")
	if matched != "4477" || got != "UK Mobile O2" {
		t.Errorf("matched prefix = %q", matched)
	}
	if tr.Len() != 5 {
		t.Errorf("len = %d", tr.Len())
	}
	if tr.Insert("44a", "bad") {
		t.Error("non-digit prefix accepted")
	}
}

func TestTrieEmpty(t *testing.T) {
	var tr Trie[int]
	if _, _, ok := tr.Match("123"); ok {
		t.Error("empty trie matched")
	}
	tr2 := New[int]()
	tr2.Insert("44", 1)
	if _, _, ok := tr2.Match("33"); ok {
		t.Error("unexpected match")
	}
}
