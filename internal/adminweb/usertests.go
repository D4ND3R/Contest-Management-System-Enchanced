// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from cms/server/admin/handlers/usertest.py and
// ContestUserTestsHandler in contestsubmission.py of CMS:
// Copyright © 2010-2013 Giovanni Mascellani <mascellani@poisson.phc.unipi.it>
// Copyright © 2010-2017 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2010-2012 Matteo Boscariol <boscarim@hotmail.com>
// Copyright © 2012-2014 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

// User tests (work C): the contest user tests page, the user test page
// and its files.

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/store/q"
)

// contestUserTestsData is the data of contest_user_tests.html.
type contestUserTestsData struct {
	*Page
	Table *utTable
}

// contestUserTestsPage is GET /contest/<id>/user_tests (CMS
// ContestUserTestsHandler).
func (s *Server) contestUserTestsPage(c *Ctx) {
	if !c.ContestFromPath() {
		return
	}
	tab, err := s.userTestTable(c, c.Contest.ID, nil, nil)
	if err != nil {
		c.Fail(err)
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("contest_user_tests.html", contestUserTestsData{Page: p, Table: tab})
}

// userTestData is the data of user_test.html.
type userTestData struct {
	*Page
	UT        q.UserTest
	User      q.User
	ContestID int32
	Task      q.Task
	Selector  *datasetSelector
	Files     []subFile
	// R is the result on the dataset shown (nil: compiling).
	R         *q.UserTestResult
	Status    string
	Compiled  bool
	Evaluated bool
	// Output is the digest of the output of the test ("" none).
	Output string
}

// userTestPage is GET /user_test/<id>[/<dataset>] (CMS UserTestHandler).
func (s *Server) userTestPage(c *Ctx) {
	ctx := c.Context()
	id := mustID(c, "user_test_id")
	det, err := s.st.Q.GetUserTestDetails(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	if !c.SetContest(det.Participation.ContestID) {
		return
	}
	t := det.Task
	var dsID int32
	if c.R.PathValue("dataset_id") != "" {
		dsID = mustID(c, "dataset_id")
	} else if t.ActiveDatasetID != nil {
		dsID = *t.ActiveDatasetID
	}
	sel, ok, err := s.loadDatasets(c, t.ID, dsID, c.URL("user_test", id))
	if err != nil {
		c.Fail(err)
		return
	}
	if !ok {
		c.NotFound()
		return
	}
	d := userTestData{UT: det.UserTest, User: det.User, ContestID: det.Participation.ContestID, Task: t, Selector: sel,
		Status: "compiling"}
	files, err := s.st.Q.AdminUserTestFiles(ctx, id)
	if err != nil {
		c.Fail(err)
		return
	}
	lang := deref(det.UserTest.Language)
	for _, name := range t.SubmissionFormat {
		for _, f := range files {
			if f.Filename == name {
				d.Files = append(d.Files, subFile{ID: f.ID, Name: s.langs.LangFilename(lang, name)})
			}
		}
	}
	r, err := s.st.Q.GetUserTestResult(ctx, q.GetUserTestResultParams{UserTestID: id, DatasetID: dsID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		c.Fail(err)
		return
	default:
		d.R = &r
		d.Status = userTestStatus(true, r.CompilationOutcome, r.EvaluationOutcome)
		d.Compiled = r.CompilationOutcome != nil
		d.Evaluated = r.EvaluationOutcome != nil
		d.Output = deref(r.Output)
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	d.Page = p
	c.Render("user_test.html", d)
}

// userTestFilePage is GET /user_test_file/<id> (CMS UserTestFileHandler).
func (s *Server) userTestFilePage(c *Ctx) {
	f, err := s.st.Q.AdminUserTestFile(c.Context(), mustID(c, "file_id"))
	if errors.Is(err, pgx.ErrNoRows) {
		c.NotFound()
		return
	}
	if err != nil {
		c.Fail(err)
		return
	}
	s.ServeFile(c, f.Digest, s.langs.LangFilename(deref(f.Language), f.Filename), "text/plain")
}
