// Package prefix implements longest-prefix matching over digit strings
// (Section 2.3). The trie is the hot-path implementation; store.LongestPrefixSQL
// is the reference implementation and the two are cross-checked in tests.
package prefix

// Trie is an immutable-after-build digit trie. The zero value is empty.
type Trie[T any] struct {
	root *node[T]
	size int
}

type node[T any] struct {
	children [10]*node[T]
	value    *T
}

// New creates an empty trie.
func New[T any]() *Trie[T] { return &Trie[T]{root: &node[T]{}} }

// Insert stores value under prefix. Non-digit characters make Insert a no-op
// and return false. An empty prefix is allowed and acts as the default route.
func (t *Trie[T]) Insert(prefix string, value T) bool {
	if t.root == nil {
		t.root = &node[T]{}
	}
	n := t.root
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if c < '0' || c > '9' {
			return false
		}
		d := c - '0'
		if n.children[d] == nil {
			n.children[d] = &node[T]{}
		}
		n = n.children[d]
	}
	if n.value == nil {
		t.size++
	}
	v := value
	n.value = &v
	return true
}

// Match returns the value stored under the longest prefix of number and the
// prefix itself. ok is false when nothing matches.
func (t *Trie[T]) Match(number string) (value T, matched string, ok bool) {
	if t.root == nil {
		return value, "", false
	}
	n := t.root
	bestLen := -1
	var best *T
	if n.value != nil {
		best, bestLen = n.value, 0
	}
	for i := 0; i < len(number); i++ {
		c := number[i]
		if c < '0' || c > '9' {
			break
		}
		n = n.children[c-'0']
		if n == nil {
			break
		}
		if n.value != nil {
			best, bestLen = n.value, i+1
		}
	}
	if best == nil {
		return value, "", false
	}
	return *best, number[:bestLen], true
}

// Len returns the number of stored prefixes.
func (t *Trie[T]) Len() int { return t.size }
