package cost

import "time"

// subscriptionPeriod returns the start and end times of the current
// subscription billing period given the provider's reset day.
func subscriptionPeriod(now time.Time, resetDay int) (start, end time.Time) {
	if resetDay < 1 {
		resetDay = 1
	}
	if resetDay > 28 {
		resetDay = 28 // safe for all months
	}

	year, month, day := now.Date()

	if day >= resetDay {
		// Current period started this month.
		start = time.Date(year, month, resetDay, 0, 0, 0, 0, now.Location())
		end = time.Date(year, month+1, resetDay, 0, 0, 0, 0, now.Location()).Add(-time.Second)
	} else {
		// Current period started last month.
		start = time.Date(year, month-1, resetDay, 0, 0, 0, 0, now.Location())
		end = time.Date(year, month, resetDay, 0, 0, 0, 0, now.Location()).Add(-time.Second)
	}
	return start, end
}
