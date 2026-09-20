package util

import (
	"fmt"

	"github.com/nekrozis/goggo/internal/config"
)

// EtaString renders a duration given in whole seconds: more than 23 hours
// renders days, positive hours render h/m/s, positive minutes render m/s, and
// anything else renders seconds. Non-positive input renders "0s".
func EtaString(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	switch {
	case hours > 23:
		days := hours / 24
		hours %= 24
		return fmt.Sprintf("%dd %02dh %02dm %02ds", days, hours, minutes, secs)
	case hours > 0:
		return fmt.Sprintf("%dh %02dm %02ds", hours, minutes, secs)
	case minutes > 0:
		return fmt.Sprintf("%dm %02ds", minutes, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// EtaFromRate renders the time remaining for bytesRemaining at rate (bytes per
// second) as bytesRemaining / rate. A non-positive rate yields "0s".
func EtaFromRate(bytesRemaining uint64, rate float64) string {
	if rate <= 0 {
		return EtaString(0)
	}
	return EtaString(int64(float64(bytesRemaining) / rate))
}

// sizeUnits returns the unit names and the base divisor for one unit format.
func sizeUnits(format uint32) (units []string, divisor float64) {
	if format == config.UnitFormatSI {
		return []string{"B", "kB", "MB", "GB", "TB", "PB"}, config.UnitDivisorKSI
	}
	return []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}, config.UnitDivisorKIEC
}

// SizeString renders bytes with two decimals. The unit chain is walked while
// the remaining value is >= the base divisor, so exactly 1024 bytes render as
// "1.00 KiB" (IEC) and 1000 as "1.00 kB" (SI). The result uses "%.2f %s".
func SizeString(bytes uint64, format uint32) string {
	units, divisor := sizeUnits(format)
	value := float64(bytes)
	unit := units[0]
	for _, u := range units {
		unit = u
		if value < divisor {
			break
		}
		value /= divisor
	}
	return fmt.Sprintf("%.2f %s", value, unit)
}

// RateString renders a byte rate. The unit selection is STRICTLY "rate >
// M-divisor uses M, otherwise K" (an exactly equal value uses the K branch). The
// result uses "%.2f%s" (no space).
func RateString(rate float64, format uint32) string {
	var divisorM, divisorK float64
	var unitM, unitK string
	if format == config.UnitFormatSI {
		divisorK, divisorM = config.UnitDivisorKSI, config.UnitDivisorMSI
		unitK, unitM = config.UnitStringKSI, config.UnitStringMSI
	} else {
		divisorK, divisorM = config.UnitDivisorKIEC, config.UnitDivisorMIEC
		unitK, unitM = config.UnitStringKIEC, config.UnitStringMIEC
	}
	if rate > divisorM {
		return fmt.Sprintf("%.2f%s", rate/divisorM, unitM+"/s")
	}
	return fmt.Sprintf("%.2f%s", rate/divisorK, unitK+"/s")
}
