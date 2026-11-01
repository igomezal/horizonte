package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func uploadPlan(s *Server, content string) (*httptest.ResponseRecorder, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if content != "" {
		fw, _ := mw.CreateFormFile("file", "plan.json")
		fw.Write([]byte(content))
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/scenarios/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return do(s, req)
}

func TestExportThenImportPlan(t *testing.T) {
	s, st := newFinanceServer(t)
	seedCashflow(t, s)
	ctx := context.Background()
	src, _ := st.CurrentPlan(ctx, "x")
	st.RenamePlan(ctx, src, "Plan de Ána")

	rec, file := do(s, httptest.NewRequest("GET", "/scenarios/1/export", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("export: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="plan-de-ana-`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if !strings.Contains(file, `"format": "horizonte/plan"`) {
		t.Fatalf("not a plan file: %.200s", file)
	}

	rec, body := uploadPlan(s, file)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("import: %d %s", rec.Code, body)
	}
	plans, _ := st.ListPlans(ctx)
	if len(plans) != 2 || plans[1].Name != "Plan de Ána" || plans[1].ParentID != nil {
		t.Fatalf("plans after import = %+v", plans)
	}
	if cur, _ := st.CurrentPlan(ctx, "x"); cur != plans[1].ID {
		t.Error("the imported plan becomes active")
	}
	orig, _ := st.PlanCounts(ctx, src)
	imp, _ := st.PlanCounts(ctx, plans[1].ID)
	for table, n := range orig {
		if imp[table] != n {
			t.Errorf("%s: imported %d, original %d", table, imp[table], n)
		}
	}
	if rec, _ := do(s, httptest.NewRequest("GET", "/scenarios/99/export", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("export of a missing plan: %d", rec.Code)
	}
}

func TestImportRejectsBadFiles(t *testing.T) {
	s, st := newFinanceServer(t)
	seedPlan(t, s)
	for _, c := range []struct{ content, want string }{
		{"", "Elige un archivo"},
		{"no soy json", "no es un plan exportado"},
		{`{"format":"horizonte/plan","version":1,"plan":{"name":"X","people":[{"id":1,"name":"Eva","birth":"2090-01","life_expectancy":90}],"assumptions":{"inflation":0.02}}}`,
			"personas: «Eva» (campo «birth»)"},
	} {
		rec, body := uploadPlan(s, c.content)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, c.want) {
			t.Errorf("upload %.30q: %d, want message %q", c.content, rec.Code, c.want)
		}
	}
	if plans, _ := st.ListPlans(context.Background()); len(plans) != 1 {
		t.Errorf("a rejected file must not create plans: %d", len(plans))
	}
}
