// Package devdash holds files shared between the repository root and the
// binary: the installer script that `devdash setup` runs on a host.
package devdash

import _ "embed"

//go:embed install.sh
var InstallScript string
