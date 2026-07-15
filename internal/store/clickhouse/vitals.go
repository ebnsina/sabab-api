package clickhouse

import (
	"context"
	"fmt"
	"time"
)

// PageVitals is one page's Core Web Vitals at p75. A zero value for a vital means
// that page had no samples of it in the window.
type PageVitals struct {
	Page    string  `json:"page"`
	LCP     float64 `json:"lcp_p75"`
	INP     float64 `json:"inp_p75"`
	CLS     float64 `json:"cls_p75"`
	Samples uint64  `json:"samples"`
}

// VitalsByPage breaks the Core Web Vitals down per page, so "which routes are
// slow for users" is answerable. It reads the metric rollups grouped by the
// `page` tag the SDK stamps on every vital, merges the quantile states across
// the window, and pivots name→column in Go.
func (db *DB) VitalsByPage(ctx context.Context, projectID uint64, from, to time.Time, rollup string, limit int) ([]PageVitals, error) {
	table, timeCol := "metrics_1m", "minute"
	if rollup == "1h" {
		table, timeCol = "metrics_1h", "hour"
	}

	q := fmt.Sprintf(`
		SELECT
			tags['page'] AS page,
			name,
			arrayElement(quantilesMerge(0.5, 0.75, 0.95, 0.99)(quantiles), 2) AS p75,
			countMerge(count) AS n
		FROM %s
		WHERE project_id = ? AND name IN ('web.lcp', 'web.inp', 'web.cls')
		  AND has(mapKeys(tags), 'page')
		  AND %s >= ? AND %s < ?
		GROUP BY page, name`, table, timeCol, timeCol)

	rows, err := db.Query(ctx, q, projectID, from, to)
	if err != nil {
		return nil, fmt.Errorf("vitals by page: %w", err)
	}
	defer rows.Close()

	byPage := map[string]*PageVitals{}
	for rows.Next() {
		var (
			page, name string
			p75        float64
			n          uint64
		)
		if err := rows.Scan(&page, &name, &p75, &n); err != nil {
			return nil, fmt.Errorf("scan page vital: %w", err)
		}
		pv := byPage[page]
		if pv == nil {
			pv = &PageVitals{Page: page}
			byPage[page] = pv
		}
		// Metric values are already in their native unit (ms for LCP/INP,
		// unitless for CLS) — unlike span durations, they are not nanoseconds.
		switch name {
		case "web.lcp":
			pv.LCP = p75
		case "web.inp":
			pv.INP = p75
		case "web.cls":
			pv.CLS = p75
		}
		// Samples is the largest per-vital sample count — a rough "how much
		// traffic backs this page's numbers".
		if n > pv.Samples {
			pv.Samples = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]PageVitals, 0, len(byPage))
	for _, pv := range byPage {
		out = append(out, *pv)
	}
	// Worst LCP first — the page hurting users most rises to the top.
	sortPageVitalsByLCP(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// sortPageVitalsByLCP orders pages by LCP descending (worst first) with a tiny
// insertion sort — the list of distinct pages is short.
func sortPageVitalsByLCP(d []PageVitals) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j].LCP > d[j-1].LCP; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}
