package slash

import (
	"strings"
	"unicode"
)

// ParseInvocation extracts the command name and the raw argument text from a
// trimmed prompt starting with a slash. It reports false for a lone slash
// with an empty token. The name runs until the first whitespace, newline or
// end of input; the raw arguments preserve inner spacing and newlines.
func ParseInvocation(input string) (name, argsRaw string, ok bool) {
	rest, found := strings.CutPrefix(input, "/")
	if !found {
		return "", "", false
	}
	end := len(rest)
	for i, r := range rest {
		if unicode.IsSpace(r) {
			end = i
			break
		}
	}
	name = rest[:end]
	if name == "" {
		return "", "", false
	}
	argsRaw = strings.TrimSpace(rest[end:])
	return name, argsRaw, true
}

// SplitArgs tokenizes raw argument text for positional placeholders. It splits
// on Unicode whitespace except inside ASCII double quotes, strips the quotes,
// and treats single quotes as ordinary characters. There is no backslash
// escaping. An unterminated quote consumes the rest of the input as one token.
func SplitArgs(s string) []string {
	var args []string
	var current strings.Builder
	inQuotes := false
	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case unicode.IsSpace(r) && !inQuotes:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return args
}

// ContainsPlaceholder reports whether the template holds at least one
// positional or all-arguments placeholder. The escape sequence alone does not
// count as a placeholder.
func ContainsPlaceholder(template string) bool {
	for i := 0; i < len(template); i++ {
		if template[i] != '$' {
			continue
		}
		if i+1 >= len(template) {
			continue
		}
		next := template[i+1]
		if next == '*' || (next >= '1' && next <= '9') {
			return true
		}
	}
	return false
}

// Expand substitutes placeholders in the template with argument text in a
// single left-to-right pass: $$ becomes a literal dollar, $* becomes the raw
// arguments, and $1 through $9 become positional tokens or empty string when
// absent. Every other dollar sequence stays literal. Argument text is never
// rescanned, so placeholders inside arguments stay literal and repetition
// expands identically. When the template holds no placeholder and the raw
// arguments are non-empty, the arguments are appended after a blank line.
func Expand(template, argsRaw string) string {
	var out strings.Builder
	out.Grow(len(template) + len(argsRaw))
	for i := 0; i < len(template); {
		if template[i] != '$' || i+1 >= len(template) {
			out.WriteByte(template[i])
			i++
			continue
		}
		next := template[i+1]
		switch {
		case next == '$':
			out.WriteByte('$')
			i += 2
		case next == '*':
			out.WriteString(argsRaw)
			i += 2
		case next >= '1' && next <= '9':
			if token := positional(argsRaw, int(next-'1')); token != "" {
				out.WriteString(token)
			}
			i += 2
		default:
			out.WriteByte('$')
			i++
		}
	}
	expanded := out.String()
	if argsRaw != "" && !ContainsPlaceholder(template) {
		return expanded + "\n\n" + argsRaw
	}
	return expanded
}

// positional returns the nth positional token of raw argument text, or empty
// string when absent.
func positional(argsRaw string, n int) string {
	tokens := SplitArgs(argsRaw)
	if n < 0 || n >= len(tokens) {
		return ""
	}
	return tokens[n]
}
