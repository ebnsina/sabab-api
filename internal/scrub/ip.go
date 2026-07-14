package scrub

import "net/netip"

// IP applies the project's address policy.
//
// The three options exist because the right answer genuinely differs by
// deployment. Keeping the full address gives the best debugging ("it is only
// failing for this one customer"); truncating keeps country-level geo while
// giving up the individual; dropping is what a GDPR-conscious install wants,
// and it must be possible to choose it without giving up the product.
func (s *Scrubber) IP(addr string) string {
	if addr == "" || s.cfg.DropIP {
		return ""
	}
	if !s.cfg.TruncateIP {
		return addr
	}

	parsed, err := netip.ParseAddr(addr)
	if err != nil {
		// Unparseable: drop it rather than store something we do not understand.
		return ""
	}
	return truncate(parsed).String()
}

// truncate zeroes the host portion of an address: the last octet of an IPv4
// (/24) and the low 80 bits of an IPv6 (/48). Both retain the network, which is
// what geo lookup actually needs, and discard the part that identifies a device.
func truncate(addr netip.Addr) netip.Addr {
	if addr.Is4() || addr.Is4In6() {
		prefix, err := addr.Unmap().Prefix(24)
		if err != nil {
			return addr
		}
		return prefix.Addr()
	}
	prefix, err := addr.Prefix(48)
	if err != nil {
		return addr
	}
	return prefix.Addr()
}
