// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
//
// Ported from OverviewHandler and ResourcesListHandler in
// cms/server/admin/handlers/contest.py and ResourcesHandler in
// cms/server/admin/handlers/main.py of CMS:
// Copyright © 2010-2017 Stefano Maggiolo <s.maggiolo@gmail.com>
// Copyright © 2012-2016 Luca Wehrstedt <luca.wehrstedt@gmail.com>

package adminweb

import (
	"context"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/worker/registry"
)

// overviewPage is / and /contest/<id>/overview (CMS OverviewHandler):
// the submissions status of the contest, the queue, the workers (their
// tables are filled by the page's script through the RPC endpoint).
func (s *Server) overviewPage(c *Ctx) {
	if c.R.PathValue("contest_id") != "" && !c.ContestFromPath() {
		return
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("overview.html", p)
}

// machine is a host of the installation on the Resource usage pages:
// this server (the administration, the database, Valkey and the
// dispatcher seen from here) or a host running workers.
type machine struct {
	// Index is its number in the URLs (CMS shard), Host its name.
	Index int
	Host  string
	// Services are its rows.
	Services []serviceStatus
}

// serviceStatus is a row of the services table of a machine.
type serviceStatus struct {
	Name string
	// Status is "running", "draining", "broken", "down" or "unknown".
	Status string
	// Since is when it started (zero: unknown).
	Since time.Time
	// Detail is a short free text (the holder of the dispatcher lease,
	// the jobs of a worker).
	Detail string
}

// machines builds the list of hosts and their services: this server
// first, then the hosts of the workers by name.
func (s *Server) machines(ctx context.Context) []machine {
	self, _ := os.Hostname()
	if self == "" {
		self = "localhost"
	}
	local := machine{Index: 0, Host: self}
	local.Services = append(local.Services, serviceStatus{Name: Name, Status: "running", Since: s.started})
	dbStatus := serviceStatus{Name: "PostgreSQL", Status: "running"}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	if err := s.pool.Ping(pctx); err != nil {
		dbStatus.Status, dbStatus.Detail = "down", err.Error()
	}
	cancel()
	local.Services = append(local.Services, dbStatus)
	kvStatus := serviceStatus{Name: "Valkey", Status: "running"}
	kctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	kvUp := true
	if err := s.kv.Ping(kctx); err != nil {
		kvStatus.Status, kvStatus.Detail, kvUp = "down", err.Error(), false
	}
	local.Services = append(local.Services, kvStatus)
	disp := serviceStatus{Name: "dispatcher", Status: "unknown"}
	var workers []registry.Info
	if kvUp {
		// The lease of the active dispatcher (internal/dispatcher/lease.go):
		// "<owner>#<epoch>".
		holder, err := s.kv.Redis().Get(kctx, s.kv.Key("dispatcher:lease")).Result()
		switch {
		case errors.Is(err, redis.Nil):
			disp.Status = "down"
		case err == nil:
			owner, _, _ := strings.Cut(holder, "#")
			disp.Status, disp.Detail = "running", owner
		}
		workers, _ = registry.List(kctx, s.kv)
	}
	local.Services = append(local.Services, disp)
	out := []machine{local}
	byHost := map[string][]serviceStatus{}
	for _, w := range workers {
		st := serviceStatus{Name: "worker " + w.Name, Status: "running", Since: w.StartedAt,
			Detail: strconv.Itoa(len(w.Jobs)) + "/" + strconv.Itoa(w.Concurrency)}
		switch {
		case w.Error != "":
			st.Status, st.Detail = "broken", w.Error
		case w.Draining:
			st.Status = "draining"
		}
		byHost[w.Host] = append(byHost[w.Host], st)
	}
	hosts := make([]string, 0, len(byHost))
	for h := range byHost {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		if h == self {
			local.Services = append(local.Services, byHost[h]...)
			out[0] = local
			continue
		}
		out = append(out, machine{Host: h, Services: byHost[h]})
	}
	for i := range out {
		out[i].Index = i
	}
	return out
}

// resourcesListData is the data of resourceslist.html.
type resourcesListData struct {
	*Page
	Machines []machine
}

// resourcesListPage is /resourceslist and /contest/<id>/resourceslist
// (CMS ResourcesListHandler): the machines.
func (s *Server) resourcesListPage(c *Ctx) {
	if c.R.PathValue("contest_id") != "" && !c.ContestFromPath() {
		return
	}
	ms := s.machines(c.Context())
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("resourceslist.html", resourcesListData{Page: p, Machines: ms})
}

// resourcesPage is /resources[/<shard>[/<contest id>]] (CMS
// ResourcesHandler): the services of one machine or of all ("all").
// CMS plots the CPU, memory and swap of each machine from
// ResourceService; here the page lists the services with their state,
// and host statistics are the monitor's (not published to the
// administration yet: the page says so).
func (s *Server) resourcesPage(c *Ctx) {
	if c.R.PathValue("contest_id") != "" && !c.ContestFromPath() {
		return
	}
	ms := s.machines(c.Context())
	if shard := c.R.PathValue("shard"); shard != "" && shard != "all" {
		n, _ := strconv.Atoi(shard)
		if n >= len(ms) {
			if c.Contest != nil {
				c.Redirect(c.URL("contest", c.Contest.ID, "resourceslist"))
			} else {
				c.Redirect(c.URL("resourceslist"))
			}
			return
		}
		ms = ms[n : n+1]
	}
	p, err := c.Page()
	if err != nil {
		c.Fail(err)
		return
	}
	c.Render("resources.html", resourcesListData{Page: p, Machines: ms})
}
