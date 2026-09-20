package zipx

import "time"

// dosDateTimeToTime converts a DOS date/time pair to a local wall-clock time
// and reports whether the decoded fields were in valid ranges. DOS timestamps
// carry no timezone; the result is deliberately in time.Local and must not be
// silently normalised to UTC in this layer.
//
//	year = 1980 + bits 15..9 (DOS base year 1980)
//	month = bits 8..5 (1..12)
//	day = bits 4..0 (1..31)
//	hour = bits 15..11 of time
//	minute= bits 10..5
//	second= 2 * bits 4..0
func dosDateTimeToTime(dosDate, dosTime uint16) (time.Time, bool) {
	year := int((dosDate>>9)&0x7f) + 1980
	month := int((dosDate >> 5) & 0x0f)
	day := int(dosDate & 0x1f)
	hour := int((dosTime >> 11) & 0x1f)
	minute := int((dosTime >> 5) & 0x3f)
	second := 2 * int(dosTime&0x1f)

	// time.Date would silently normalise out-of-range components, so validate
	// first.
	switch {
	case year > 2107: // tm_year <= 207 <=> year <= 2107
		return time.Time{}, false
	case month < 1 || month > 12:
		return time.Time{}, false
	case day < 1 || day > 31:
		return time.Time{}, false
	case hour > 23:
		return time.Time{}, false
	case minute > 59:
		return time.Time{}, false
	case second > 59:
		return time.Time{}, false
	}

	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.Local), true
}
