package ip

import (
	"bytes"
	"fmt"
	"net"
	"strings"

	"log"
)

// Range specifies range of IPs with From & To included in this range.
type Range struct {
	From net.IP
	To   net.IP
}

//Between - test to determine if a given ip is between two others (inclusive)
func Between(from net.IP, to net.IP, test net.IP) bool {
	if from == nil || to == nil || test == nil {
		log.Print("An IP input is nil") // or return an error!?
		return false
	}

	from16 := from.To16()
	to16 := to.To16()
	test16 := test.To16()
	if from16 == nil || to16 == nil || test16 == nil {
		log.Print("An IP did not convert to a 16 byte") // or return an error!?
		return false
	}

	if bytes.Compare(test16, from16) >= 0 && bytes.Compare(test16, to16) <= 0 {
		return true
	}
	return false
}

// CIDRToRange - convert CIDR format to range from & to IP
// eg. "62.76.47.12/28" makes 62.76.47.0 - 62.76.47.15
func CIDRToRange(cidr string) (string, string, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", "", err
	}

	var ips []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		ips = append(ips, ip.String())
	}
	return ips[0], ips[len(ips)-1], nil
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// ParseIPRangeString - parse string in form 1.2.3.4/24 or 127.0.0.1 or 1.2.3.4-1.2.4.0
func ParseIPRangeString(rangeStr string) (net.IP, net.IP, error) {
	subRange := strings.Split(rangeStr, "-")
	if len(subRange) > 2 || len(rangeStr) == 0 {
		return nil, nil, fmt.Errorf("invalid IP range string format: %s", rangeStr)
	}
	// no "-" sign
	if len(subRange) == 1 {
		s := strings.TrimSpace(subRange[0])
		// 127.0.0.1 - one IP format
		if strings.Index(s, "/") == -1 {
			ip := net.ParseIP(s)
			if ip == nil {
				return nil, nil, fmt.Errorf("invalid IP format: %s", s)
			}
			return ip, ip, nil
		}

		// 127.0.0.1/24 format
		sFrom, sTo, err := CIDRToRange(s)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid IP format: %s, err: %v", s, err)
		}
		ipFrom := net.ParseIP(sFrom)
		ipTo := net.ParseIP(sTo)
		if ipFrom == nil {
			return nil, nil, fmt.Errorf("invalid IP format: %s", sFrom)
		}
		if ipTo == nil {
			return nil, nil, fmt.Errorf("invalid IP format: %s", sTo)
		}

		return ipFrom, ipTo, nil
	}

	if len(subRange) == 2 {
		sFrom := strings.TrimSpace(subRange[0])
		sTo := strings.TrimSpace(subRange[1])

		ipFrom := net.ParseIP(sFrom)
		ipTo := net.ParseIP(sTo)
		if ipFrom == nil {
			return nil, nil, fmt.Errorf("invalid IP format: %s", sFrom)
		}
		if ipTo == nil {
			return nil, nil, fmt.Errorf("invalid IP format: %s", sTo)
		}

		from16 := ipFrom.To16()
		to16 := ipTo.To16()
		if bytes.Compare(from16, to16) == 1 {
			return nil, nil, fmt.Errorf("invalid IP range string format: %s, %s > %s", rangeStr, ipFrom.String(), ipTo.String())
		}
		return ipFrom, ipTo, nil
	}

	return nil, nil, fmt.Errorf("weird - shouldn't be here: invalid range IP format: %s", rangeStr)
}
