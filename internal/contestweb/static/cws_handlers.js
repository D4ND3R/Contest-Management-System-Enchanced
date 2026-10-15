/* Contest Management System (Go fork)
 * SPDX-License-Identifier: AGPL-3.0-or-later
 * Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
 *
 * The event handlers that the templates of CMS write inline (onclick=,
 * onchange=...), moved here because the Content Security Policy of this
 * fork forbids inline handlers (D10). Each one does exactly what the
 * attribute did in cms/server/contest/templates; they are bound by
 * delegation, so they also work on content loaded later (live regions).
 */

"use strict";

$(document).on("change", "#lang", function() {
    // contest.html: <select id="lang" onchange="utils.switch_lang()">
    utils.switch_lang();
});
