package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/frankgraave/subglance/internal/configfile"
)

// maxConfigFileBytes bounds an uploaded configuration file. A thousand fully
// specified monitors come to well under a megabyte, so four leaves room while
// still refusing a body that could only be a mistake or an attack.
const maxConfigFileBytes = 4 << 20

// importMu serialises imports. Two imports of overlapping files running at
// once would each plan against a state the other is about to change, and the
// second would create the objects the first just created.
var importMu sync.Mutex

// handleExportConfig serves the instance's configuration as YAML.
//
// Editors and administrators only, although nothing in the file is secret: the
// export assigns and stores a key for every object that does not have one yet,
// which is a write, and a viewer does not write.
//
// It takes importMu because assigning keys is a write that an import running
// at the same time could collide with.
func (s *Server) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	user, _ := UserFromContext(r.Context())
	importMu.Lock()
	doc, err := s.exportConfig(r.Context(), time.Now(), user.Role.CanAdmin())
	importMu.Unlock()
	if err != nil {
		s.log.Error("export configuration", "error", err)
		writeError(w, http.StatusInternalServerError, "could not export the configuration")
		return
	}
	body, err := configfile.Marshal(doc)
	if err != nil {
		s.log.Error("encode configuration", "error", err)
		writeError(w, http.StatusInternalServerError, "could not export the configuration")
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subglance-config.yaml"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleImportConfig creates and updates objects from a YAML document.
//
// With ?dry_run=true it reports what it would do and writes nothing. Without
// it, it does the same planning first and writes only when the whole file is
// valid. It never deletes anything that the file does not mention.
func (s *Server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	dryRun := false
	if v := r.URL.Query().Get("dry_run"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, fieldProblem("dry_run", "dry_run must be true or false"))
			return
		}
		dryRun = b
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigFileBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge,
			"the file is larger than "+strconv.Itoa(maxConfigFileBytes>>20)+" MiB")
		return
	}
	doc, err := configfile.Parse(body)
	if err != nil {
		writeConfigProblem(w, err)
		return
	}
	// Status pages are administered by administrators only. The whole file
	// is refused rather than imported without its pages, because a report
	// that quietly skipped them would read as if they had been imported.
	if user, _ := UserFromContext(r.Context()); len(doc.StatusPages) > 0 && !user.Role.CanAdmin() {
		writeProblem(w, http.StatusForbidden, fieldProblem("status_pages",
			"only an administrator can import status pages; remove status_pages from the file, or import it as an administrator"))
		return
	}

	importMu.Lock()
	defer importMu.Unlock()

	ctx := r.Context()
	plan, err := s.planImport(ctx, doc)
	if err != nil {
		var p *configfile.Problem
		if errors.As(err, &p) {
			writeConfigProblem(w, err)
			return
		}
		s.log.Error("plan configuration import", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the current configuration")
		return
	}
	plan.report.DryRun = dryRun
	if dryRun {
		writeJSON(w, http.StatusOK, plan.report)
		return
	}

	// A client that disconnects halfway must not stop the writes between an
	// object and its key.
	if err := s.applyImport(context.WithoutCancel(ctx), &plan, func(token string) string { return pushURL(r, token) }); err != nil {
		s.log.Error("apply configuration import", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.log.Info("configuration imported",
		"created", plan.report.Summary.Create, "updated", plan.report.Summary.Update,
		"unchanged", plan.report.Summary.Unchanged, "needs_secrets", plan.report.Summary.NeedsSecrets)
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, plan.report)
}

// writeConfigProblem reports a mistake in a file, with the place it was found
// in the field member so a client can point at it.
func writeConfigProblem(w http.ResponseWriter, err error) {
	var p *configfile.Problem
	if errors.As(err, &p) {
		writeProblem(w, http.StatusBadRequest, problem{field: p.Path, msg: p.Msg})
		return
	}
	writeProblem(w, http.StatusBadRequest, bodyProblem(err.Error()))
}
