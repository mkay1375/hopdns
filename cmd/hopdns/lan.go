package main

import (
	"net"
	"net/netip"
)

func detectLANIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	// A connected UDP socket reveals the route's source IP without sending traffic.
	var preferred netip.Addr
	if conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(1, 1, 1, 1), Port: 53}); err == nil {
		preferred, _ = netip.AddrFromSlice(conn.LocalAddr().(*net.UDPAddr).IP)
		preferred = preferred.Unmap()
		_ = conn.Close()
	}
	var fallback string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() {
				continue
			}
			if prefix.Addr() == preferred {
				return preferred.String()
			}
			if fallback == "" {
				fallback = prefix.Addr().String()
			}
		}
	}
	return fallback
}
