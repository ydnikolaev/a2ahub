// Package datatransport is the transport-driver seam (driver.go carries
// this package's own canonical doc comment; this file exists only for the
// ADR-001/ADR-023 import-boundary declaration below).
//
// ADR-001 import row, unchanged by ADR-023 (docs/decisions.md): datapackage
// alone, and only for the shared unsafe-locator sentinel — this package
// never grows a second reason to depend on it.
package datatransport
