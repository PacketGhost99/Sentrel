package app

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sentrel/server/metrics"
)

// maxExportRows caps the rows (distinct timestamps) of one metrics export.
const maxExportRows = 50000

type exportSeries struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Unit   string            `json:"unit"`
	Points []metrics.Point   `json:"points"`
}

// metricUnit infers the display unit from the metric name.
func metricUnit(name string) string {
	switch {
	case strings.HasSuffix(name, "_percent"):
		return "percent"
	case strings.HasSuffix(name, "_bytes_per_sec"):
		return "bytes/s"
	case strings.HasSuffix(name, "_bytes"):
		return "bytes"
	case strings.HasSuffix(name, "_bps"):
		return "bits/s"
	case strings.HasSuffix(name, "_celsius"):
		return "celsius"
	case strings.HasSuffix(name, "_ms"):
		return "ms"
	case strings.HasSuffix(name, "_dbm"):
		return "dBm"
	case strings.HasSuffix(name, "_seconds"):
		return "seconds"
	}
	return "number"
}

// seriesColumn names a series as metric{label=value,...} with sorted labels.
func seriesColumn(metric string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return metric
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + labels[k]
	}
	return metric + "{" + strings.Join(parts, ",") + "}"
}

// parseExportRange reads range=<seconds> or start/end/step. Unlike
// parseRange it does not silently coarsen the step; the caller enforces the cap.
func parseExportRange(c *Ctx) (time.Time, time.Time, time.Duration, error) {
	end := time.Now()
	start := end.Add(-time.Hour)
	if v := c.Q("range"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return start, end, 0, badRequest("range must be a positive number of seconds")
		}
		start = end.Add(-time.Duration(n) * time.Second)
	} else {
		if v := c.Q("end"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return start, end, 0, badRequest("invalid end")
			}
			end = time.Unix(int64(f), 0)
		}
		start = end.Add(-time.Hour)
		if v := c.Q("start"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				start = time.Unix(int64(f), 0)
			} else if d, err := time.ParseDuration(v); err == nil {
				start = end.Add(-d)
			} else {
				return start, end, 0, badRequest("invalid start")
			}
		}
	}
	if !start.Before(end) {
		return start, end, 0, badRequest("start must be before end")
	}
	var step time.Duration
	if v := c.Q("step"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			step = d
		} else if n, err := strconv.Atoi(v); err == nil {
			step = time.Duration(n) * time.Second
		} else {
			return start, end, 0, badRequest("invalid step")
		}
		if step < time.Second {
			return start, end, 0, badRequest("step must be at least 1s")
		}
	} else {
		step = metrics.AutoStep(start, end)
	}
	if rows := int64(end.Sub(start)/step) + 1; rows > maxExportRows {
		min := end.Sub(start) / (maxExportRows - 1)
		min = (min/time.Second + 1) * time.Second
		return start, end, 0, badRequest("export would have %d rows (limit %d); use a coarser step, for example step=%ds, or a shorter range", rows, maxExportRows, int(min.Seconds()))
	}
	return start, end, step, nil
}

var exportNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (a *App) apiHostMetricsExport(c *Ctx) (any, error) {
	h, err := c.visibleHost(c.ID())
	if err != nil {
		return nil, err
	}
	format := strings.ToLower(c.Q("format"))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		return nil, badRequest("format must be csv or json")
	}
	start, end, step, err := parseExportRange(c)
	if err != nil {
		return nil, err
	}
	panelIDs := make([]string, 0, len(hostPanels))
	for id := range hostPanels {
		panelIDs = append(panelIDs, id)
	}
	sort.Strings(panelIDs)
	out := []exportSeries{}
	seen := map[string]bool{}
	for _, id := range panelIDs {
		for _, m := range hostPanels[id].Metrics {
			if seen[m] {
				continue
			}
			seen[m] = true
			sel := fmt.Sprintf(`%s%s{host_id="%d"}`, metrics.Prefix, m, h.ID)
			q := sel
			if step > time.Minute {
				q = fmt.Sprintf("avg_over_time(%s[%ds])", sel, int(step.Seconds()))
			}
			ser, err := a.ts.QueryRange(c.ctx(), q, start, end, step)
			if err != nil {
				return nil, badRequest("query: %v", err)
			}
			for _, s := range ser {
				if len(s.Points) == 0 {
					continue
				}
				lbl := map[string]string{}
				for k, v := range s.Labels {
					if k == "__name__" || k == "host_id" || k == "tenant_id" {
						continue
					}
					lbl[k] = v
				}
				out = append(out, exportSeries{Name: seriesColumn(m, lbl), Labels: lbl, Unit: metricUnit(m), Points: s.Points})
			}
		}
	}
	base := exportNameRe.ReplaceAllString(h.Name, "_")
	if base == "" {
		base = "host"
	}
	fname := fmt.Sprintf("%s-metrics-%s.%s", base, time.Now().UTC().Format("20060102-1504"), format)
	if format == "json" {
		return rawResponse(func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"host": h.Name, "start": start.Unix(), "end": end.Unix(), "step": int(step.Seconds()), "series": out})
		}), nil
	}
	return rawResponse(func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
		writeMetricsCSV(w, out)
	}), nil
}

// writeMetricsCSV writes one row per timestamp with a column per series.
func writeMetricsCSV(w http.ResponseWriter, ser []exportSeries) {
	tsSet := map[int64]struct{}{}
	for _, s := range ser {
		for _, p := range s.Points {
			tsSet[int64(p[0])] = struct{}{}
		}
	}
	xs := make([]int64, 0, len(tsSet))
	for t := range tsSet {
		xs = append(xs, t)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	idx := make(map[int64]int, len(xs))
	for i, t := range xs {
		idx[t] = i
	}
	cols := make([][]string, len(ser))
	for i, s := range ser {
		col := make([]string, len(xs))
		for _, p := range s.Points {
			col[idx[int64(p[0])]] = strconv.FormatFloat(p[1], 'g', -1, 64)
		}
		cols[i] = col
	}
	cw := csv.NewWriter(w)
	hdr := []string{"timestamp", "unix"}
	for _, s := range ser {
		hdr = append(hdr, s.Name)
	}
	_ = cw.Write(hdr)
	row := make([]string, len(hdr))
	for i, t := range xs {
		row[0] = time.Unix(t, 0).UTC().Format(time.RFC3339)
		row[1] = strconv.FormatInt(t, 10)
		for j := range ser {
			row[2+j] = cols[j][i]
		}
		_ = cw.Write(row)
	}
	cw.Flush()
}
