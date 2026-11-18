// Package engine embeds the reviewed upstream proxy command in the HopDNS binary.
package engine

import "github.com/mosajjal/sniproxy/v2/enginecli"

// Run blocks until the engine receives a shutdown signal. Call it only in a
// dedicated child process so upstream global handlers and ACLs remain isolated.
func Run(args []string) {
	enginecli.Run(args)
}
