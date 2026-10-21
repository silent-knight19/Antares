package server

// globMatch reports whether value matches pattern with Redis's stringmatchlen
// semantics: *, ?, [abc], [a-z], [^abc], and backslash escapes.
func globMatch(pattern, value []byte) bool {
	skipLongerMatches := false
	return globMatchImpl(pattern, value, &skipLongerMatches, 0)
}

// globMatchImpl mirrors Redis's stringmatchlen_impl, including its early exit
// when a later '*' has already failed to match the rest of the string.
func globMatchImpl(pattern, value []byte, skipLongerMatches *bool, nesting int) bool {
	if nesting > 1000 {
		return false
	}
	p, v := 0, 0
	for p < len(pattern) && v < len(value) {
		plen := len(pattern) - p
		switch pattern[p] {
		case '*':
			for plen > 0 && p+1 < len(pattern) && pattern[p+1] == '*' {
				p++
				plen--
			}
			if plen == 1 {
				return true
			}
			for v < len(value) {
				if globMatchImpl(pattern[p+1:], value[v:], skipLongerMatches, nesting+1) {
					return true
				}
				if *skipLongerMatches {
					return false
				}
				v++
			}
			*skipLongerMatches = true
			return false
		case '?':
			v++
		case '[':
			p++
			plen--
			negate := plen > 0 && pattern[p] == '^'
			if negate {
				p++
				plen--
			}
			matched := false
			for {
				if plen >= 2 && pattern[p] == '\\' {
					p++
					plen--
					if pattern[p] == value[v] {
						matched = true
					}
				} else if plen > 0 && pattern[p] == ']' {
					break
				} else if plen == 0 {
					p--
					plen++
					break
				} else if plen >= 3 && pattern[p+1] == '-' {
					start, end := pattern[p], pattern[p+2]
					if start > end {
						start, end = end, start
					}
					if value[v] >= start && value[v] <= end {
						matched = true
					}
					p += 2
					plen -= 2
				} else if pattern[p] == value[v] {
					matched = true
				}
				p++
				plen--
			}
			if negate {
				matched = !matched
			}
			if !matched {
				return false
			}
			v++
		case '\\':
			if plen >= 2 {
				p++
				plen--
			}
			fallthrough
		default:
			if pattern[p] != value[v] {
				return false
			}
			v++
		}
		p++
		if v == len(value) {
			for p < len(pattern) && pattern[p] == '*' {
				p++
			}
			break
		}
	}
	return p == len(pattern) && v == len(value)
}
