package reportshttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/IQVO/customer-returns/internal/domain/returnsperformance"
)

// Wire bodies (camelCase, apis/openapi.yaml tag `Reports`).

type dayBody struct {
	Day                     string   `json:"day"`
	ReturnsAuthorized       int      `json:"returnsAuthorized"`
	ReturnsReceived         int      `json:"returnsReceived"`
	TimeToReceiveSamples    int      `json:"timeToReceiveSamples"`
	TimeToReceiveP50Seconds *float64 `json:"timeToReceiveP50Seconds"`
	TimeToReceiveP95Seconds *float64 `json:"timeToReceiveP95Seconds"`
}

type reasonBody struct {
	Reason          string  `json:"reason"`
	Returns         int     `json:"returns"`
	Share           float64 `json:"share"`
	CumulativeShare float64 `json:"cumulativeShare"`
}

type performanceBody struct {
	From         string        `json:"from"`
	To           string        `json:"to"`
	Days         []dayBody     `json:"days"`
	ReasonPareto []reasonBody  `json:"reasonPareto"`
	Freshness    freshnessBody `json:"freshness"`
}

func performance(rg returnsperformance.Range, days []returnsperformance.DayRow, pareto []returnsperformance.ParetoRow, f returnsperformance.Freshness) performanceBody {
	out := performanceBody{
		From: rg.FirstDay(), To: rg.LastDay(),
		Days: make([]dayBody, 0, len(days)), ReasonPareto: make([]reasonBody, 0, len(pareto)),
		Freshness: freshnessBody{AsOf: f.AsOf, LagSeconds: f.LagSeconds},
	}
	for _, d := range days {
		out.Days = append(out.Days, dayBody{
			Day: returnsperformance.FormatDay(d.Day), ReturnsAuthorized: d.Authorized, ReturnsReceived: d.Received,
			TimeToReceiveSamples: d.Samples, TimeToReceiveP50Seconds: d.P50, TimeToReceiveP95Seconds: d.P95,
		})
	}
	for _, p := range pareto {
		out.ReasonPareto = append(out.ReasonPareto, reasonBody{Reason: p.Reason, Returns: p.Returns, Share: p.Share, CumulativeShare: p.CumulativeShare})
	}
	return out
}

type freshnessBody struct {
	AsOf       *time.Time `json:"asOf"`
	LagSeconds *float64   `json:"lagSeconds"`
}

// problem is one RFC 9457 problem type: its status, slug and title.
type problem struct {
	status int
	slug   string
	title  string
}

var (
	invalidRange          = problem{http.StatusBadRequest, "invalid-report-range", "The report range is invalid"}
	notFound              = problem{http.StatusNotFound, "not-found", "Resource not found"}
	methodNotAllowed      = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable"}
	internalError         = problem{http.StatusInternalServerError, "internal-error", "Internal error"}
)

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemBody{Type: p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath()})
}

// logSafe strips line breaks so a logged value cannot forge a log line.
var logSafe = strings.NewReplacer("\n", " ", "\r", " ")

// writeInternal logs err and answers a 500 whose detail never leaks it.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "report query failed", "error", logSafe.Replace(err.Error()), "path", logSafe.Replace(r.URL.Path))
	writeProblem(w, r, internalError, "an unexpected error occurred")
}
