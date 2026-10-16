# Markdown corpus

Inputs and the HTML that CMS's markdown filter gives for them:
markdown-it-py 3.0.0 with the `js-default` preset (`MarkdownIt("js-default").render(text)`),
the library and preset of `cms/server/jinja2_toolbox.py`. `TestMarkdownCorpus`
checks `web.Markdown` against them.

- `commonmark_spec.json`: the 652 examples of the CommonMark specification 0.31.2
  (the inputs only, as in goldmark's `_test/spec.json`; the outputs are
  markdown-it-py's, not the specification's). The CommonMark specification is
  © John MacFarlane, licensed under CC BY-SA 4.0.
- `cases.json`: inputs written for this fork (links, autolinks, host names,
  strikethrough runs, Unicode whitespace, dangerous URLs, tables...).

To regenerate the outputs, run markdown-it-py 3.0.0 over the inputs, for
example with the virtualenv of a CMS installation:

```python
import json, markdown_it
md = markdown_it.MarkdownIt("js-default")
for f in ["cases.json", "commonmark_spec.json"]:
    cases = json.load(open(f))
    for c in cases:
        c["out"] = md.render(c["in"])
    json.dump(cases, open(f, "w"), ensure_ascii=False, indent=0)
```
