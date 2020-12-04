package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"contrib.go.opencensus.io/exporter/prometheus"
	"github.com/go-logr/logr"
	prom "github.com/prometheus/client_golang/prometheus"
	"go.opencensus.io/metric/metricdata"
	"go.opencensus.io/metric/metricexport"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	networkIODistributionMs    = view.Distribution(0.01, 0.05, 0.1, 0.3, 0.6, 0.8, 1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1000, 2000, 5000, 10000, 20000, 30000, 50000, 100000, 200000, 500000, 1000000, 2000000, 5000000, 10000000, 10000000)
	blockSizeDistributionBytes = view.Distribution(1<<7, 1<<8, 1<<9, 1<<10, 1<<11, 1<<12, 1<<13, 1<<14, 1<<15, 1<<16, 1<<18, 1<<19, 1<<20, 1<<21, 1<<22, 1<<23, 1<<24, 1<<25)
)

var cacheTag, _ = tag.NewKey("cache")

var (
	fillDuration = stats.Float64("fill_duration_ms", "Time taken to fill the cache with a block", stats.UnitMilliseconds)
	fillSize     = stats.Int64("fill_size_bytes", "Size of block retrieved for fill", stats.UnitBytes)
	fillRequest  = stats.Int64("fill_request", "Number of fill requests", stats.UnitDimensionless)
	fillFailure  = stats.Int64("fill_failure", "Number of failed fills", stats.UnitDimensionless)
	fillSuccess  = stats.Int64("fill_success", "Number of successful fills", stats.UnitDimensionless)

	getDuration = stats.Float64("get_duration_ms", "Time taken to get a block via the cache", stats.UnitMilliseconds)
	getSize     = stats.Int64("get_size_bytes", "Size of block retrieved for get", stats.UnitBytes)
	getRequest  = stats.Int64("get_request", "Number of get requests", stats.UnitDimensionless)
	getMiss     = stats.Int64("get_miss", "Number of get requests that were not in the cache", stats.UnitDimensionless)
	getHit      = stats.Int64("get_hit", "Number of get requests that were satisfied from the cache", stats.UnitDimensionless)
	getFailure  = stats.Int64("get_failure", "Number of get requests that failed", stats.UnitDimensionless)

	gonudbRecordCount = stats.Int64("gonudb_record_count", "Number of records reported by the gonudb store", stats.UnitDimensionless)
	gonudbRate        = stats.Float64("gonudb_rate_bytes_per_second", "Date write rate reported by the gonudb store", stats.UnitDimensionless)
)

func startTimer(ctx context.Context, m *stats.Float64Measure) func() {
	start := time.Now()
	return func() {
		elapsedms := time.Since(start).Seconds() * 1000
		stats.Record(ctx, m.M(elapsedms))
	}
}

func reportEvent(ctx context.Context, m *stats.Int64Measure) {
	stats.Record(ctx, m.M(1))
}

func reportSize(ctx context.Context, m *stats.Int64Measure, v int) {
	stats.Record(ctx, m.M(int64(v)))
}

func cacheContext(ctx context.Context, name string) context.Context {
	ctx, _ = tag.New(ctx, tag.Upsert(cacheTag, name))
	return ctx
}

func initMetricReporting(reportingInterval time.Duration) error {
	view.SetReportingPeriod(reportingInterval)

	metricViews := []*view.View{
		{
			Name:        fillRequest.Name() + "_total",
			Measure:     fillRequest,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        fillFailure.Name() + "_total",
			Measure:     fillFailure,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        fillSuccess.Name() + "_total",
			Measure:     fillSuccess,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        fillSize.Name() + "_total",
			Measure:     fillSize,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        fillSize.Name(),
			Measure:     fillSize,
			Aggregation: blockSizeDistributionBytes,
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        fillDuration.Name() + "_total",
			Measure:     fillDuration,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        fillDuration.Name(),
			Measure:     fillDuration,
			Aggregation: networkIODistributionMs,
			TagKeys:     []tag.Key{cacheTag},
		},

		{
			Name:        getRequest.Name() + "_total",
			Measure:     getRequest,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        getFailure.Name() + "_total",
			Measure:     getFailure,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        getHit.Name() + "_total",
			Measure:     getHit,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        getMiss.Name() + "_total",
			Measure:     getMiss,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        getSize.Name() + "_total",
			Measure:     getSize,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        getSize.Name(),
			Measure:     getSize,
			Aggregation: blockSizeDistributionBytes,
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        getDuration.Name() + "_total",
			Measure:     getDuration,
			Aggregation: view.Sum(),
			TagKeys:     []tag.Key{cacheTag},
		},
		{
			Name:        getDuration.Name(),
			Measure:     getDuration,
			Aggregation: networkIODistributionMs,
			TagKeys:     []tag.Key{cacheTag},
		},

		{
			Name:        gonudbRecordCount.Name(),
			Measure:     gonudbRecordCount,
			Aggregation: view.LastValue(),
		},
		{
			Name:        gonudbRate.Name(),
			Measure:     gonudbRate,
			Aggregation: view.LastValue(),
		},
	}

	return view.Register(metricViews...)
}

func registerPrometheusExporter(namespace string) (*prometheus.Exporter, error) {
	registry := prom.NewRegistry()
	registry.MustRegister(prom.NewGoCollector(), prom.NewProcessCollector(prom.ProcessCollectorOpts{}))

	pe, err := prometheus.NewExporter(prometheus.Options{
		Namespace: namespace,
		Registry:  registry,
	})
	if err != nil {
		return nil, err
	}

	view.RegisterExporter(pe)

	return pe, nil
}

func NewMetricLogger(logger logr.Logger) *MetricLogger {
	return &MetricLogger{
		logger: logger,
		reader: metricexport.NewReader(),
	}
}

var _ metricexport.Exporter = (*MetricLogger)(nil)

type MetricLogger struct {
	logger logr.Logger
	reader *metricexport.Reader
}

func (l *MetricLogger) Log() {
	if l.logger.Enabled() {
		l.reader.ReadAndExport(l)
	}
}

func (l *MetricLogger) ExportMetrics(ctx context.Context, metrics []*metricdata.Metric) error {
	counts := map[string]map[string]int64{}

	for _, m := range metrics {
		for _, ts := range m.TimeSeries {
			labels := make([]string, 0, len(ts.LabelValues))
			for _, lv := range ts.LabelValues {
				if lv.Present {
					labels = append(labels, lv.Value)
				} else {
					labels = append(labels, "")
				}
			}

			label := strings.Join(labels, ",")

			for _, p := range ts.Points {
				switch v := p.Value.(type) {
				case int64:
					c, ok := counts[label]
					if !ok {
						c = map[string]int64{}
					}
					c[m.Descriptor.Name] = v
					counts[label] = c
				}
			}
		}
	}

	lbls := make([]string, 0, len(counts))
	for lbl := range counts {
		lbls = append(lbls, lbl)
	}

	sort.Strings(lbls)
	for _, lbl := range lbls {
		c := counts[lbl]
		if lbl != "" {
			hitRate := float64(c["get_hit_total"]) / float64(c["get_request_total"])
			errorRate := float64(c["get_failure_total"]) / float64(c["get_request_total"])
			l.logger.Info(lbl, "requests", c["get_request_total"], "hits", c["get_hit_total"], "failures", c["get_failure_total"], "hit_rate", fmt.Sprintf("%0.2f", hitRate), "error_rate", fmt.Sprintf("%0.2f", errorRate), "sent_bytes", c["get_size_bytes_total"])

			if _, exists := c["fill_request_total"]; exists {
				l.logger.Info(lbl, "fills", c["fill_request_total"], "fill_bytes", c["fill_size_bytes_total"])
			}
		} else if _, exists := c["gonudb_record_count"]; exists {
			l.logger.Info("gonusb", "records", c["gonudb_record_count"])
		}
	}

	return nil
}
