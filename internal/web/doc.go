// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package web is the core shared by the contestant site (internal/contestweb,
// CMS ContestWebServer) and the administration (internal/adminweb, CMS
// AdminWebServer): templates with the semantics of CMS's Jinja2
// environment, the per-request language, signed cookies, CSRF, static
// assets, file downloads from the blob store, Markdown, rate limiting and
// the response helpers. It ports cms/server/{jinja2_toolbox.py, util.py,
// file_middleware.py}, cms/server/contest/jinja2_toolbox.py, the language
// choice of cms/locale/locale.py and BaseHandler.setup_locale, and
// cmscommon/mimetypes.py of CMS.
//
// # Wiring a site
//
//	st, _ := web.NewStatic(web.SharedStatic, siteStatic) // site files override shared ones
//	langs, _ := web.NewLanguages(web.LanguagesOptions{
//		Catalogs: locale.FS,
//		Domains:  []string{locale.DomainContest, locale.DomainAdmin}, // "cms", then "admin"
//		Locale:   func(code string) web.Locale { return l10n.Get(code) },
//	})
//	eng, _ := web.NewEngine(web.EngineOptions{
//		Templates: siteTemplates, // fs.FS with base.html, contest.html, macro/…
//		Static:    st,
//		Funcs: func(cur func() *web.Render) template.FuncMap {
//			return template.FuncMap{"contest_url": func(c ...any) string {
//				return cur().Extra.(*state).ContestURL.Join(c...)
//			}}
//		},
//	})
//	csrf := web.NewCSRF(web.CSRFOptions{Exempt: hasAuthorizationHeader})
//	site := &web.Site{Engine: eng, Languages: langs, CSRF: csrf,
//		Location: serverTimeZone, Log: log, ErrorPage: "error.html"}
//	cookies := web.NewSigner(cfg.DeriveKey("contest-web/cookie"))
//
//	mux.Handle("GET /static/", http.StripPrefix("/static", st))
//	handler := csrf.Protect(mux) // every POST needs the _xsrf token
//
// and in a handler:
//
//	ch := langs.Choose(r, langs.Filter(contest.AllowedLocalizations))
//	rc := site.NewRender(w, r, ch.Lang) // also sets Content-Language
//	rc.Extra = &state{ContestURL: rc.Root.Sub(contest.Name)}
//	site.Page(w, r, rc, http.StatusOK, "overview.html", data)
//
// # Templates (Engine)
//
// Pages are html/template files parsed once at startup. Jinja constructs
// of the CMS templates map as follows:
//
//	{% extends "contest.html" %}       {{/* extends "contest.html" */}} (at the top,
//	                                   after the license comment if any)
//	{% block core %}…{% endblock %}    {{define "core"}}…{{end}} in a child,
//	                                   {{block "core" .}}…{{end}} in a parent
//	{% trans %}Logout{% endtrans %}    {{T "Logout"}}
//	{% trans n=x %}…%(n)s…{% endtrans %}  {{Format (T "…%(n)s…") "n" .X}}
//	{% trans count=n %}…{% pluralize %}…  {{Format (Tn "…" "…" .N) "count" .N}}
//	{{ _("x") }}, gettext("x")         {{T "x"}}
//	{{ x|format_datetime }} and co.    {{format_datetime .X}}
//	{{ url("tasks", t.name) }}         {{url "tasks" .Name}}
//	{{ static_url("static", "a.css") }} {{static "a.css"}} (adds ?h=<hash>)
//	{{ xsrf_form_html|safe }}          {{xsrf_form_html}}
//	<script>                           <script nonce="{{nonce}}">
//	translation.identifier             {{lang}}
//	{% import 'macro/x.html' as m %}   files under macro/ are visible to
//	{{ m.rows(…) }}                    every page: {{template "x.rows" (dict "k" v)}}
//	{% include "row.html" %}           {{template "row.html" .}}
//	{% set page = "overview" %}        a field of the page data
//	onclick="…" (inline handlers)      moved to the site's JS files (CSP, D10)
//
// The output keeps the whitespace of CMS: lines holding only control
// actions vanish as with Jinja's trim_blocks and lstrip_blocks, and the
// final newline of every file is dropped. A page ported line by line
// renders to the bytes CMS sends (TestCMSPageBytes), except strings inside
// <script>, which html/template escapes differently ("\/", "\u002b").
//
// Functions (see Engine.funcs for the full list): T, Tn, Tc, Format, lang,
// translation, now, timezone, url, static, nonce, xsrf_form_html,
// xsrf_token, format_datetime, format_time, format_datetime_smart,
// format_timedelta, format_duration, format_size, format_decimal,
// format_locale, format_status_text, today, mime_name, all, any,
// dictselect, format (Python's %), markdown, make_timestamp, mimetype,
// mime_icon, dict, list, add, sub, mul, div, join, replace, default,
// tojson, comment. Request-dependent functions read the Render passed to
// Engine.Execute; sites add their own (contest_url, get_score_class,
// format_token_rules…) through EngineOptions.Funcs.
//
// Escaping: T, Tn and Tc return template.HTML, as Jinja's {% trans %}
// marks the catalog text safe; Format escapes the values it inserts.
// Catalogs are trusted (they are part of the source); user text must
// never be passed as a msgid or as Format's message.
//
// Messages come from the "cms" catalogs (the msgids of CMS, with its 25
// translations) and fall back to "admin" for strings CMS does not have
// (Lang.T); `make generate` extracts the contestant site's strings into
// admin.pot leaving out those already in cms.pot (cmd/i18n-extract -skip).
//
// Formats come from a Locale (internal/l10n implements it with CMS's
// Babel/CLDR data); BasicLocale is an English fallback for tests.
//
// webtest.RenderPages renders every page of a site (zero data by default)
// and fails on crashes; webtest.AddXSRF lets test requests through CSRF.
//
// # Languages
//
// Languages.Filter applies a contest's allowed_localizations (CMS
// filter_language_codes) and Languages.Choose picks the language as
// CMS's setup_locale: the best match of Accept-Language (Werkzeug's
// parsing, Babel's negotiate_locale), else the first available one, and
// the "language" cookie of the selector overrides it. Choice.ByID lists
// the selector's languages in CMS's order.
//
// # Security
//
//   - CSRF (Tornado's xsrf_cookies, which the CMS templates and scripts
//     use): the _xsrf cookie, the _xsrf form field or the X-XSRFToken /
//     X-CSRFToken header, version 2 masked tokens, constant-time
//     comparison, every method but GET, HEAD and OPTIONS; plus
//     Sec-Fetch-Site must not be cross-site (nor same-site, by default).
//     The _xsrf cookie is readable by JavaScript (the admin scripts send
//     it as a header), as in CMS; it is SameSite=Lax.
//   - Cookies (SetCookie, Signer): HttpOnly, SameSite=Lax, Path=/, Secure
//     when the request came over HTTPS directly or per X-Forwarded-Proto
//     from num_proxies trusted proxies (httpx.IsHTTPS). Signer signs a
//     value bound to the cookie name and a time with HMAC-SHA256 and a
//     key derived from secret_key (config.DeriveKey with a purpose, D5);
//     Verify rejects tampering, another name or key, and expired values.
//   - Redirects: NormalizeNextPage only accepts local paths and rebuilds
//     them relative to the site (no open redirects); Redirect keeps
//     relative Locations (sites behind a proxy under a sub-path) and
//     strips line breaks.
//   - CSP: httpx.SecureHeaders sets the header and the nonce;
//     {{nonce}} prints it.
//   - Limits: ParseForm and ReadJSON cap the body (ErrBodyTooLarge, 413);
//     RateLimiter is a token bucket per key with bounded memory for logins
//     and submissions (TooManyRequests sends 429 with Retry-After).
//
// # Static files and downloads
//
// Static holds the files in memory, hashed (SHA-256, 24 hex digits as
// CMS's StaticFileHasher) and gzipped when that helps: a URL carrying the
// current ?h= is cached for a year (immutable), others revalidate with
// ETag/Last-Modified. SharedStatic is CMS's cms/server/static (jQuery 3.6.0
// and jQuery Migrate 3.3.2, MIT licensed, the favicon and loading.gif of
// CMS, AGPL) plus live.js, the browser side of internal/live shared by
// the three sites (connection indicator, live regions, fallback to
// polling; its header documents the data- attributes it reads).
//
// ServeBlob is CMS's FileServerMiddleware: a file streamed from the blob
// store with the digest as ETag, Cache-Control: no-cache, private,
// Content-Disposition with the file name (Werkzeug's quoting, plus RFC
// 8187 filename* for non-ASCII names), conditional and range requests;
// 404 for unknown digests and 503 for the tombstone. MimeType, MimeIcon
// and MimeComment reproduce CMS's guesses from file names, the generic
// icons of static/img/mimetypes and the localized type names from
// shared-mime-info, from data generated by mimedata/extract.py.
//
// # Responses
//
// Engine.Render buffers the page and sends it with Content-Type text/html;
// charset=UTF-8, Cache-Control: no-cache, must-revalidate (CMS sets it on
// every handler) and, for 200 answers to GET, a SHA-1 ETag with 304
// answers as Tornado. Site.Page falls back to the error page (Site.Error,
// CMS write_error with ErrorData{StatusCode}) when a template fails, and
// PlainError sends CMS's "A critical error has occurred :-(" text.
// WriteJSON is CMS's json() helper; Arg, Args, ArgDefault and BoolArg read
// arguments as Tornado's get_argument (last value, control characters
// replaced, stripped).
package web
