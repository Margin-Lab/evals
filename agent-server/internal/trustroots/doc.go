// Package trustroots owns the bundled trust store used by managed Node bootstrap.
//
// The embedded public root bundle lives in public-roots.pem and is materialized
// under state/ so Go HTTPS downloads and managed Node/npm child processes use
// the same trust roots. The public bundle is intended to be refreshed manually
// when the repo updates its pinned trust set.
package trustroots
