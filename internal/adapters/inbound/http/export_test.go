package http

import "net/http"

// ProblemTypeForTest is one problem type of the catalogue, for the external test package.
type ProblemTypeForTest struct {
	Slug   string
	Status int
	Class  string
}

// ProblemTypesForTest is the whole catalogue (allProblemTypes), one entry per slug and status.
func ProblemTypesForTest() []ProblemTypeForTest {
	var out []ProblemTypeForTest
	for _, p := range allProblemTypes() {
		out = append(out, ProblemTypeForTest{Slug: p.Slug, Status: p.Status, Class: string(p.Class)})
	}
	return out
}

// RefuseForTest answers the problem type (slug, status) through the production error writer: a
// typed error of the catalogue through writeError, any other problem through writeProblem, so the
// class marking the middleware reads is the production one.
func RefuseForTest(w http.ResponseWriter, r *http.Request, slug string, status int) {
	for _, e := range problemCatalogue {
		if e.p.slug == slug && e.p.status == status {
			writeError(w, r, e.err)
			return
		}
	}
	for _, p := range standaloneProblems {
		if p.slug == slug && p.status == status {
			writeProblem(w, r, p, "refused for the test")
			return
		}
	}
	panic("no problem type " + slug)
}
