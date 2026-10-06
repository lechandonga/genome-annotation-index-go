package parse

import "fmt"

func formatSprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
