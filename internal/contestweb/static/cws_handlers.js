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

$(document).on("change", ".task_submissions #submit_solution input[type=file]:not(#input_zip)", function() {
    // task_submissions.html: <input type="file" ... onchange="CMS.CWSUtils.filter_languages(
    //     $(this).parents('form').find('select[name=language] option'),
    //     $(this).parents('form').find('input[type=file]'), TASK_LANGUAGES)"/>
    CMS.CWSUtils.filter_languages($(this).parents('form').find('select[name=language] option'),
                                  $(this).parents('form').find('input[type=file]'), TASK_LANGUAGES);
});

$(document).on("change", ".test_interface .submit_test input[type=file]:not(#input_file):not(#input_zip)", function() {
    // test_interface.html: <input type="file" ... onchange="CMS.CWSUtils.filter_languages(
    //     $(this).parents('form').find('select[name=language] option'),
    //     $(this).parents('form').find('input[type=file]').not('#input_file'),
    //     TASK_LANGUAGES['<task name>'])"/>, in the tab "test_<task name>".
    var task = $(this).parents('.tab-pane').attr('id').substring("test_".length);
    CMS.CWSUtils.filter_languages(
        $(this).parents('form').find('select[name=language] option'),
        $(this).parents('form').find('input[type=file]').not('#input_file'),
        TASK_LANGUAGES[task]);
});

// task_submissions.html and test_interface.html poll the status of the
// submissions and user tests that are not final when the page loads (CMS
// schedule_update_scores, schedule_update_user_test_row). The rows that a
// live refresh brings in (live.js) poll in the same way.
function pending_user_tests(fn) {
    $('.user_test_list tbody tr[data-status][data-status!="2"][data-status!="4"]').each(function () {
        fn($(this).parent().parent().attr("data-task"), $(this).attr("data-user-test"));
    });
}

$(document).ready(function () {
    if (typeof schedule_update_user_test_row === "function") {
        // The page's own script polls these.
        var polled = schedule_update_user_test_row.polled = schedule_update_user_test_row.polled || {};
        pending_user_tests(function (task, id) {
            polled[task + "/" + id] = true;
        });
    }
});

document.addEventListener("cms:live-refreshed", function () {
    if (typeof schedule_update_scores === "function") {
        var delays = schedule_update_scores.delays || {};
        $('.submission_list tbody tr[data-status][data-status!="2"][data-status!="5"]').each(function () {
            var id = $(this).attr("data-submission");
            if (!delays[id]) {
                schedule_update_scores(id);
            }
        });
    }
    if (typeof schedule_update_user_test_row === "function") {
        var polled = schedule_update_user_test_row.polled = schedule_update_user_test_row.polled || {};
        pending_user_tests(function (task, id) {
            if (!polled[task + "/" + id]) {
                polled[task + "/" + id] = true;
                schedule_update_user_test_row(task, id);
            }
        });
    }
});
