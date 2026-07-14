package migrate

import "strings"

// splitStatements breaks a migration file into individual statements on
// semicolons. ClickHouse's protocol accepts exactly one statement per Exec, so
// unlike Postgres we cannot hand it the whole file.
//
// It skips semicolons inside single-quoted strings, backtick-quoted
// identifiers, line comments (-- and #) and block comments (/* */), which is
// what separates it from a strings.Split that would corrupt any DDL containing
// a semicolon in a default expression or comment.
func splitStatements(sql string) []string {
	var (
		statements []string
		current    strings.Builder
	)
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			statements = append(statements, s)
		}
		current.Reset()
	}

	const (
		code = iota
		inSingleQuote
		inBacktick
		inLineComment
		inBlockComment
	)
	state := code

	runes := []rune(sql)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		next := func() rune {
			if i+1 < len(runes) {
				return runes[i+1]
			}
			return 0
		}

		switch state {
		case inSingleQuote:
			current.WriteRune(c)
			switch {
			case c == '\\' && next() != 0: // escaped char — consume both
				i++
				current.WriteRune(runes[i])
			case c == '\'':
				state = code
			}

		case inBacktick:
			current.WriteRune(c)
			if c == '`' {
				state = code
			}

		case inLineComment:
			// Comments are dropped, but the newline is kept so the statement
			// stays readable in error messages.
			if c == '\n' {
				current.WriteRune(c)
				state = code
			}

		case inBlockComment:
			if c == '*' && next() == '/' {
				i++
				state = code
			}

		default: // code
			switch {
			case c == '\'':
				state = inSingleQuote
				current.WriteRune(c)
			case c == '`':
				state = inBacktick
				current.WriteRune(c)
			case c == '-' && next() == '-':
				i++
				state = inLineComment
			case c == '#':
				state = inLineComment
			case c == '/' && next() == '*':
				i++
				state = inBlockComment
			case c == ';':
				flush()
			default:
				current.WriteRune(c)
			}
		}
	}
	flush()
	return statements
}
