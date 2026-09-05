//go:build windows

package supervisor

import "fmt"

// reclaimPortLsof reports a clear error on windows: lsof does not exist and
// the SIGINT→grace→SIGKILL kill contract is unix-specific. Colliding ports
// therefore fall through to the normal "port in use" error + backoff path
// instead of killing the occupant.
func reclaimPortLsof(local int) error {
	return fmt.Errorf("port reclamation is not supported on windows")
}
