// Package privacy serves the relay's privacy statement. The page is static
// and versioned with the code, so a change to it goes through the same
// review and tests as everything else.
package privacy

import (
	_ "embed"
	"net/http"
)

//go:embed privacy.html
var page []byte

// Handler serves the privacy statement as a static HTML page.
func Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page)
}
