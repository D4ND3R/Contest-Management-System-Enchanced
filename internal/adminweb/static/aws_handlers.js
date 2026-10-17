/* Contest Management System (Go fork)
 * SPDX-License-Identifier: AGPL-3.0-or-later
 * Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors
 *
 * The event handlers that the templates of CMS's administration write
 * inline (onclick=, onchange=), moved here because the Content Security
 * Policy of this fork forbids inline handlers (D10). The templates keep
 * their elements and replace the attribute with data- attributes that say
 * the same thing; each handler below does exactly what the attribute did.
 * They are bound by delegation, so they also work on content loaded later
 * (live regions).
 *
 *   onclick="CMS.AWSUtils.ajax_delete('URL');"     data-ajax-delete="URL"
 *   onclick="CMS.AWSUtils.ajax_post('URL');"       data-ajax-post="URL"
 *   onclick="utils.show_file('NAME','URL')"        data-show-file="NAME" data-url="URL"
 *   onclick="javascript:history.back();"           data-history-back
 *   onclick="document.FORM.submit();"              data-submit-form="FORM"
 *   onclick="utils.render_markdown_preview(this)"  data-markdown-preview
 *   onclick="utils.question_reply_toggle(event, this);"  data-question-reply-toggle
 *   onchange="utils.update_additional_answer(event, this);"  data-additional-answer
 *   onclick="utils.do_diff()"                      data-do-diff
 *   onclick="confirm('Q') && cmsrpc_request('S', 0, 'M', ARGS,
 *            function(response) { utils.redirect_if_ok('NEXT', response); });"
 *       data-rpc-service="S" data-rpc-method="M" data-rpc-args='ARGS as JSON'
 *       data-rpc-next="NEXT" data-confirm="Q" (data-confirm optional; without
 *       data-rpc-next the page is reloaded)
 *   onclick="return confirm('Q');"                 data-confirm="Q" (alone:
 *       a click on a link or a submit button goes on only if confirmed)
 */

"use strict";

$(document).on("click", "[data-ajax-delete]", function (event) {
    event.preventDefault();
    CMS.AWSUtils.ajax_delete($(this).attr("data-ajax-delete"));
});

$(document).on("click", "[data-ajax-post]", function (event) {
    event.preventDefault();
    CMS.AWSUtils.ajax_post($(this).attr("data-ajax-post"));
});

$(document).on("click", "[data-show-file]", function (event) {
    event.preventDefault();
    utils.show_file($(this).attr("data-show-file"), $(this).attr("data-url"));
});

$(document).on("click", "[data-history-back]", function (event) {
    event.preventDefault();
    history.back();
});

$(document).on("click", "[data-submit-form]", function (event) {
    event.preventDefault();
    document.forms[$(this).attr("data-submit-form")].submit();
});

$(document).on("click", "[data-markdown-preview]", function () {
    utils.render_markdown_preview(this);
});

$(document).on("click", "[data-question-reply-toggle]", function (event) {
    utils.question_reply_toggle(event, this);
});

$(document).on("change", "[data-additional-answer]", function (event) {
    utils.update_additional_answer(event, this);
});

$(document).on("click", "[data-do-diff]", function () {
    utils.do_diff();
});

$(document).on("click", "[data-rpc-service]", function (event) {
    event.preventDefault();
    var el = $(this);
    var question = el.attr("data-confirm");
    if (question !== undefined && !confirm(question)) {
        return;
    }
    var args = {};
    try {
        args = JSON.parse(el.attr("data-rpc-args") || "{}");
    } catch (e) {
        args = {};
    }
    var next = el.attr("data-rpc-next");
    cmsrpc_request(el.attr("data-rpc-service"), 0, el.attr("data-rpc-method"), args,
                   function (response) {
                       utils.redirect_if_ok(next !== undefined ? next : window.location.href, response);
                   });
});

$(document).on("click", "a[data-confirm]:not([data-rpc-service]), button[data-confirm]:not([data-rpc-service]), input[data-confirm]:not([data-rpc-service])", function (event) {
    if (!confirm($(this).attr("data-confirm"))) {
        event.preventDefault();
        event.stopImmediatePropagation();
    }
});
