package web

import "strconv"

// intToStr formats an int for templ text nodes without fmt in the template.
func intToStr(n int) string { return strconv.Itoa(n) }
