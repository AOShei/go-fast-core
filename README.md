# go-fast-core

The shared document model and extractor interface for the go-fast-* extraction libraries. Standard library only.

Every extractor (`go-fast-pdf`, and later ooxml, html, email, ...) returns the same `*gofastcore.Document`, so chunkers, indexers and knowledge-graph builders are written once and work for every format.

```go
import "github.com/AOShei/go-fast-core" // package gofastcore
```

**Status: schema version 0.1, agreed with the PDF extractor over two review rounds on 2026-11-18.** Not yet tagged. It will be tagged v0.1.0 once `go-fast-pdf` has been ported to it.

## Series conventions

- **Naming.** Every package carries the brand prefix: `gofastcore`, `gofastpdf`, `gofasthtml`. No package in the series shares a name with a standard library package, so callers never need an import alias.
- **Layout.** A library's public API is in its module root. Implementation goes under `internal/`, the CLI under `cmd/`. No `pkg/` directory.
- **Format-specific settings.** `Options` holds only what every format understands. Settings for one format (for PDF: memory mapping, a cache budget) are fields on that library's extractor value: `gofastpdf.Extractor{...}.Extract(...)`.
- **Dependencies.** The standard library and other go-fast modules only. `go-fast-core` itself imports only the standard library. Any other dependency must be challenged.
- **Layering.** The shared model is the common denominator across formats. An extractor may offer a richer native API beside it (for PDF: word and line boxes, font names and sizes, glyph positions). Those details stay out of core.
- **Preserve content.** Anything that is real content, whether it carries meaning or provides context, is preserved where possible. Normalizing a representation is fine (a ligature to its letters, `\r\n` to `\n`); dropping or flattening something the author put there is not. This is why tabs are kept in text, running headers and footers are tagged instead of removed, and an attachment that cannot be extracted is still listed.
- **Flag hidden text, never drop it.** Text a reader cannot see is extracted like any other and marked with the reason it is hidden. The libraries report the fact; whether it is an attack or a scanned page's text layer is the caller's judgement. See [Hidden text](#hidden-text).
- **No OCR.** The go-fast libraries extract what the file contains. They report what they found and leave the next step to the caller.

## The model

```
Document
├── Source      format, name, MIME, size
├── Metadata    title, authors, dates, language, extra
├── Outline[]   the source's own table of contents (PDF bookmarks, epub nav)
├── HiddenChecked  which hidden-text checks ran
├── Parts[]     the format's natural unit: page, slide, sheet, chapter, message, body
│   ├── Blocks[]    content in reading order
│   └── Warnings[]  what went wrong in this part
├── Children[]  embedded documents (attachments, archive members), each a full Document
└── Warnings[]  what went wrong for the document as a whole
```

A block has a `Kind`, usually `Text`, and kind-specific fields:

| Kind | Text | Level | Other fields |
|---|---|---|---|
| `heading` | heading text | 1–6 | |
| `paragraph` | paragraph text | | |
| `list_item` | item text without the marker | nesting depth, from 1 | `Ordered`, `Marker` |
| `table` | empty | | `Table` (rows of cell strings, header row count, merges) |
| `image` | alt text or description | | `Image` (id, MIME, pixel size, optional bytes) |
| `code` | verbatim code | | `Attrs["language"]` |
| `quote` | quoted text | nesting depth, from 1 | |
| `caption` | caption text | | |
| `footnote` | note text | | `Marker` |
| `formula` | LaTeX or plain text | | |
| `note` | speaker note or comment | | |
| `header`, `footer` | running page header/footer | | |

Every block may also carry:

- `Origin`: `native` when the source declares the block's kind, level and boundaries (a docx style, an HTML tag, a tagged-PDF element, a PDF outline entry), `inferred` when the extractor deduced them from geometry or fonts. Absent in JSON means `native`. It does not cover the block's position in the reading order: a PDF image is `native` even though where it falls among the text is inferred.
- `Hidden`: the reasons text in this block is not visible to a reader of the source. Absent in JSON means none was found. See [Hidden text](#hidden-text).
- `Continued`: this block continues the last block of the same kind in the previous part, such as a paragraph split by a page break.
- `Loc`: where it came from. `BBox` for paged formats, `Range` (A1 notation) for sheets, `Offset`/`Length` for plain-text formats. This is what lets a retrieval hit be cited.
- `Spans`: inline bold/italic/link ranges over `Text` (optional), and the ranges that are hidden (always present when part of a block is hidden).
- `Attrs`: format-specific key/values with no dedicated field.

### Design decisions

- **Flat blocks, no section tree.** Hierarchy is implied by heading levels. Extractors emit in one pass and page workers stay independent.
- **Parts are self-contained.** A part owns its blocks and its warnings. A page worker fills one `Part` with no shared state and no locking, and a part handed on by itself is complete.
- **Structure first, Markdown derived.** `Markdown()` and `Text()` render the blocks. Extractors never emit Markdown strings.
- **Provenance, not confidence.** `Origin` says whether structure was declared or guessed. There is no numeric score: heuristics do not produce calibrated probabilities. A consumer that needs certainty treats `inferred` blocks as plain text and chunks by size.
- **Tables are `[][]string`.** Merged cells put their text top-left and are listed in `Merges`. Numbers and dates appear as the source displays them.
- **Furniture is kept but tagged.** Running headers and footers are `header`/`footer` blocks; the renderers drop them by default.
- **Partial results beat errors.** A document that is 95% readable returns with warnings. An error means nothing usable came out.

## Extractor contract

1. All strings are valid UTF-8 and follow the text conventions below.
2. A block is at least a paragraph-sized unit, never a fragment of a line or word.
3. Blocks are in reading order. Where the format does not declare order (PDF), order is the extractor's best inference.
4. A top-level document has at least one part.
5. `Part.Number` is the part's 1-based ordinal in the source. Numbers strictly increase but need not be contiguous: extracting pages 40–60 yields parts numbered 40 to 60.
6. A block that runs across a part boundary is either assigned to the part where it starts or split, with `Continued` set on the later piece.
7. `Table` is set exactly on `table` blocks, `Image` exactly on `image` blocks.
8. `Extract` is safe to call from many goroutines and honors context cancellation.
9. Hidden text is extracted and flagged, never dropped. Every extractor calls `part.MarkHidden()` on each finished part and records in `Document.HiddenChecked` which reasons it fully checked.
10. `Document.Parts`, `Part.Blocks` and `Table.Rows` are never nil, so JSON has `[]` and never `null`. A table has at least one row and one column.
11. Output is deterministic: the same input and options give the same document whatever `Options.Workers` is, including where a limit truncates it.

`doc.Validate()` checks everything mechanical in rules 1, 4, 5 and 7, plus spans, tables, merges, warnings and enum values. Run it on every test fixture and fuzz output:

```go
doc, err := gofastpdf.Extract(ctx, f, size, nil)
if err != nil { t.Fatal(err) }
if err := doc.Validate(); err != nil { t.Fatal(err) }
```

### Text conventions

- Text is in logical order, the order in which it is read and typed, also for right-to-left scripts. An extractor that cannot guarantee this for some input says so with a warning.
- Soft line breaks inside a paragraph are joined with a single space. A `\n` in `Text` means a hard break.
- End-of-line hyphenation is resolved only when the extractor is certain. Otherwise the hyphen stays and the lines are joined with no space: `inter-` + `national` gives `inter-national`, so a real compound such as `well-` + `known` comes out right.
- Presentation-form ligatures (U+FB00 to U+FB06) are expanded to their letters.
- No string contains a control character (NUL, form feed, carriage return, ...) other than `\n` and `\t`.
- Block text is not empty and has no leading or trailing whitespace.
- Code blocks are verbatim: the ligature and whitespace rules do not apply to them.
- Unicode normalization form is not guaranteed (normalization is not in the standard library).

`Validate` enforces the rules it can check mechanically: no control characters in any string, no unexpanded ligatures in block text or table cells, and trimmed non-empty block text.

### JSON

`parts`, `blocks` and `rows` are always arrays, `[]` when empty and never `null`, so a consumer can iterate them without a nil check on a failed page or an unextracted child.

The model has no custom JSON marshalers, which keeps encoding at the full speed of `encoding/json`. The guarantee comes from the lists never being nil: `New` creates an empty parts list, `part.MarkHidden()` makes an empty blocks list, and a table has at least one row and one column. `Validate` rejects a nil list.

### Coordinates

`Part.Width`/`Height` and `Loc.BBox` describe the part **as displayed**: origin top-left, y increasing downward. For PDF that is the CropBox after `/Rotate` and `UserUnit` are applied, in points. A box drawn at `BBox` on a rendered image of the page encloses the block.

### Span offsets are bytes

`Span.Start` and `Span.End` are **byte offsets into the UTF-8 text**, not character offsets. They always fall on character boundaries. Languages that index strings by code point must convert. In Python:

```python
text.encode("utf-8")[span["start"]:span["end"]].decode("utf-8")
```

### Images

`Image.ID` identifies the image resource, not the block. Equal IDs mean equal bytes, and several blocks may share one ID (a logo on every page). With `ImagesData`, the bytes are guaranteed only on the first block with a given ID in document order; repeats may omit them.

## Warnings

A warning is a fact about what happened. It never recommends an action.

```json
{"code": "part.image_only", "severity": "warning", "message": "1 image, no text operators"}
```

Warnings local to a part are in `Part.Warnings`; those about the whole file are in `Document.Warnings`. `doc.AllWarnings()` iterates both, and `part.Has(code)` tests one part.

| Severity | Meaning |
|---|---|
| `info` | Something notable happened; the output is complete |
| `warning` | The output is present but degraded, or may not represent what a reader of the source would see |
| `error` | Content is missing or untrustworthy |

Shared codes (constants `Warn*`):

| Code | What happened |
|---|---|
| `part.failed` | The part could not be parsed; its blocks are missing or incomplete |
| `part.no_text` | The part was parsed and contains no text and no images |
| `part.image_only` | The part was parsed and contains no text, but does contain image content. Reported whatever `Options.Images` is set to |
| `text.hidden` | The part contains text a reader of the source cannot see; the blocks carry the reasons. Severity `info` when the only reason is a scan's text layer |
| `text.unmapped_glyphs` | Some text has no Unicode mapping in the source and appears as U+FFFD or is absent |
| `image.unsupported_encoding` | Image data was requested and an image uses an encoding the extractor cannot deliver; its block has no data |
| `input.repaired` | The input was structurally damaged and was read by a recovery path |
| `limit.exceeded` | A limit was reached during extraction; the output is truncated there |
| `child.not_extracted` | An embedded document is listed but was not extracted |
| `child.unsupported` | No extractor handles the embedded document's format, or it uses an unimplemented feature |
| `child.encrypted` | The embedded document is encrypted and was not opened (no password, or the wrong one) |
| `child.failed` | Extraction of an embedded document failed for any other reason |

An extractor may add codes of its own under a format prefix, such as `pdf.xref_rebuilt`.

This is how a caller handles scanned pages without go-fast knowing anything about OCR:

```go
for i := range doc.Parts {
    p := &doc.Parts[i]
    if p.Has(gofastcore.WarnPartImageOnly) {
        sendToMyOCR(path, p.Number) // the caller's decision, not ours
    }
}
```

## Hidden text

Text that a reader of the source cannot see still reaches whatever consumes the extracted text. Planting instructions that way is a known method of poisoning the context of an AI system. Hidden text also has ordinary uses: the text layer of a scanned page, screen-reader text, a collapsed menu, a lookup sheet.

The go-fast libraries do not decide which it is. They **extract hidden text like any other and flag it with the reason it is not visible**, so the caller can review, quarantine or exclude it.

**A flag is evidence that text is hidden. No flag is not evidence that a document is safe.** An extractor cannot see text inside images, a font whose glyphs display one thing and map to another, or visibility controlled by external stylesheets and scripts.

### Reasons

| Reason | Meaning |
|---|---|
| `invisible` | The source marks the text as not displayed: PDF invisible render mode or a layer that is off; Word hidden-text formatting; a hidden slide, sheet, row or column; HTML `display:none`, `visibility:hidden`, `hidden` |
| `text_layer` | Unpainted text lying within an image that covers at least half of its page (`HiddenTextLayerCoverage`), which is how a scanned page's searchable text is stored. Nothing verifies that it matches the image. It stands alone: see below |
| `tiny` | Displayed smaller than 2 points (`HiddenTinySize`) |
| `low_contrast` | Text color indistinguishable from what is behind it: WCAG contrast ratio under 1.2 (`HiddenContrastRatio`, `LowContrast`) |
| `off_page` | Positioned outside the visible area of the page, slide or viewport |
| `clipped` | Outside a clipping region, including a box too small to show the text with overflow cut off |
| `occluded` | Covered by an image or a filled shape |
| `transparent` | Drawn with zero opacity |
| `invisible_chars` | Contains characters that display as nothing yet can carry a payload (`InvisibleChars`) |

The thresholds live in core so that a reason means the same in every format.

A block flagged `text_layer` carries none of the reasons that describe how painted text fails to be seen (`invisible`, `tiny`, `low_contrast`, `clipped`, `occluded`, `transparent`), whichever way the file hides it, including text drawn first and covered by the page image. That keeps its warning at `info` and makes "exclude everything hidden except text layers" work. Unpainted text over a smaller image is `invisible`. `Validate` enforces the rule.

`invisible_chars` is not one of those reasons. A text layer that contains invisible characters is flagged `text_layer+invisible_chars`, and its warning is `warning`, not `info`: smuggled characters are worth reporting wherever they are.

`invisible_chars` covers Unicode tag characters (which mirror ASCII invisibly and which language models can read), the bidirectional overrides U+202D and U+202E, runs of four or more zero-width characters, and runs of two or more variation selectors. Single zero-width characters and well-formed emoji sequences are ordinary text and are not flagged. Core runs this scan for every format, plain text included.

### Where the flag is

- **`Block.Hidden`** lists every reason found in the block. `b.Hidden != 0` is the only test needed to find hidden content.
- **Part of a block.** When only a passage is hidden, a `Span` with `Hidden` set marks the range; for a table, a `HiddenCells` region marks the cells. A reason that appears on a span or region applies only there. A reason that appears on none applies to the whole block. `b.HiddenWhole()` and `b.VisibleText(exclude)` do this arithmetic.
- **`Part.Hidden`** marks a whole hidden sheet or slide; its blocks carry the same reasons.
- **One `text.hidden` warning per part** that has any, so "does this document contain hidden text?" is one loop over the parts.

```json
{"kind": "paragraph", "hidden": "low_contrast",
 "text": "Visible one. Hidden sentence. Visible two.",
 "spans": [{"start": 13, "end": 29, "hidden": "low_contrast"}]}
```

A reason added by a newer extractor decodes as `unknown` and is kept, so hidden text never looks visible to an older consumer.

### What was checked

No library can check every reason: plain text has no colors, HTML cannot know what covers what, and PDF's contrast check needs `Options.DeepHidden`. So that the absence of a flag can be audited, every document records which checks ran:

```json
{"schema_version": "0.1", "source": {"format": "pdf"},
 "hidden_checked": "invisible+text_layer+tiny+off_page+transparent+invisible_chars", ...}
```

- A reason **listed** in `Document.HiddenChecked` was checked completely, as far as that extractor is able (its documentation states the limits). A block without that flag was looked at and nothing was found.
- A reason **not listed** was not checked, or only in part. It may still appear on a block where a cheaper check happened to find it, but its absence says nothing.
- `invisible_chars` is always listed. `Validate` requires it.

With this, "no hidden text was found" and "hidden text was not looked for" are different, machine-readable answers.

### Using it

```go
for part, b := range doc.AllBlocks() {
    if b.Hidden != 0 {
        review(part.Number, b.Hidden, b.Text) // the caller's decision
    }
}
```

`Markdown()` and `Text()` include hidden text by default: flagging does not block extraction. To leave it out of rendered output:

```go
// Everything hidden:
doc.WriteText(w, &gofastcore.RenderOptions{ExcludeHidden: gofastcore.HiddenAny})

// Everything hidden except the text layer of scanned pages,
// which would otherwise render empty:
doc.WriteText(w, &gofastcore.RenderOptions{
    ExcludeHidden: gofastcore.HiddenAny &^ gofastcore.HiddenTextLayer,
})
```

Exclusion cuts exactly the flagged ranges, so smuggled invisible characters are stripped while the visible sentence around them stays.

### For extractor authors

1. Set the reasons only your format can know, on the block, span, table region or part.
2. Never merge hidden text into a visible block without marking the range.
3. Call `part.MarkHidden()` once per finished part, including a failed or blank one. It runs the invisible-character scan, copies `Part.Hidden` to the blocks, adds the `text.hidden` warning and makes an empty blocks list non-nil. `Validate` fails if it was skipped.
4. Set `doc.HiddenChecked` to the reasons you checked completely in this run, always including `HiddenChars`. List a reason only if its full check ran: one that depends on `Options.DeepHidden` is listed only when that option was set and the check exists.
5. Cheap checks always run. Checks that need extra work (knowing what is drawn behind or over the text) run when `Options.DeepHidden` is set. Document which is which.
6. Metadata strings cannot carry a block flag. If `InvisibleChars` finds something in one, add a document-level `text.hidden` warning.

## Extractor interface

```go
type Extractor interface {
    Formats() []string
    Extract(ctx context.Context, r io.ReaderAt, size int64, opts *Options) (*Document, error)
}
```

`Options` (zero value: text and structure only, whole input, all CPUs):

| Field | Purpose |
|---|---|
| `Workers` | Goroutines inside one document; 0 means all CPUs |
| `Images` | `ImagesNone`, `ImagesMeta` (position and size), `ImagesData` (bytes too) |
| `Spans` | Ask for inline formatting and links |
| `DeepHidden` | Also run the hidden-text checks that cost extra work (contrast against drawn backgrounds, occlusion, clipping) |
| `Password` | Tried on encrypted input, and passed down to embedded documents |
| `Parts` | `PartRange{First, Last}`, inclusive part numbers; zero value means all; a range that selects nothing is `ErrNoPartsSelected` |
| `Limits` | `MaxInputBytes`, `MaxDecodedBytes`, `MaxParts`, `MaxDepth`. Zero means the extractor's default; negative means no limit |
| `Child` | Function that extracts embedded documents; see below |
| `Name` | Recorded as `Source.Name` |

### Errors

Extractors wrap these, so test with `errors.Is`: `ErrPasswordRequired`, `ErrWrongPassword`, `ErrUnsupported`, `ErrMalformed`, `ErrLimitExceeded`, `ErrNoPartsSelected`.

`ErrLimitExceeded` means a limit stopped the extraction before there was anything to return. That is always so for `MaxInputBytes`, and it is so for any other limit reached before the input could even be identified (a decompression bomb in the part that says what the file is). A limit reached once there is a document truncates it and adds a `limit.exceeded` warning with severity `error`; an error is never returned together with a document. `MaxParts` returns the first `MaxParts` parts of the requested range, also where the part count is known up front.

### Embedded documents

An email attachment may be a PDF; a PDF may embed a spreadsheet. Extractors do not import each other. The caller injects a dispatcher as `Options.Child`, and an extractor hands every embedded file to `opts.ExtractChild(...)`, appending the result to `Children`:

```go
doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mime, reader, size))
```

`ExtractChild` always returns a document, so every embedded file is at least listed with its name and size. When the child was not extracted, the result has no parts and one warning whose code says why: `child.not_extracted` (no `Child` supplied, or depth limit reached), `child.unsupported`, `child.encrypted` or `child.failed`. The last three are chosen from the error the child extractor returned, so a dispatcher should return `ErrUnsupported` for formats it has no extractor for.

## Schema evolution

`schema_version` changes when a field is removed or its meaning changes. Additions do not change it, including new block kinds: a decoder that meets a kind, part kind, origin or severity it does not know maps it to that enum's `Unknown` value, and unknown style names are ignored. An unknown block renders as a paragraph. Re-encoding such a document writes `"unknown"`, so the original name is lost. `Validate` rejects `Unknown`, since extractors must never emit it.

## How each format maps

| Format | Part kind | Notes |
|---|---|---|
| pdf | `page` | `BBox` on blocks; structure is `inferred` unless the PDF is tagged or the outline confirms a heading |
| docx, odt, rtf | one `body` | Heading styles give levels; comments become `note` |
| pptx, odp | `slide` | `Name` is the slide title; speaker notes become `note` |
| xlsx, ods, csv | `sheet` | `Name` is the sheet name; each used region is a `table` with `Loc.Range` |
| html, md, txt | one `body` | `Loc.Offset`/`Length` into the source |
| epub | `chapter` | One per spine item; nav becomes `Outline` |
| eml, msg, mbox | `message` | Headers in `Metadata.Extra`; attachments in `Children` |

## Porting go-fast-pdf

This is a clean break: no compatibility shim for the old `pkg/loader` API or JSON.

| Today (`go-fast-pdf/pkg/model`) | Shared model |
|---|---|
| `Document.Pages[]` | `Document.Parts[]` with `Kind: PartPage` |
| `Page.PageNumber` | `Part.Number` (the page's ordinal, also when a range is extracted); a page label such as `iv` goes in `Part.Name` |
| `Page.Width`, `Height` | `Part.Width`, `Height`, as displayed (CropBox after rotation) |
| `Page.Content` (one string) | `Part.Blocks[]`; `part.Markdown()` or `part.Text()` gives a string |
| `Page.CharCount` | Dropped; derivable from the blocks |
| `Page.Images[]` | `image` blocks placed in reading order |
| `Image.ID` (`Im1`, page-local) | `Image.ID` from the object number (`obj-12`); synthetic for inline images |
| `Image.Width`, `Height` | Same, now `int` |
| `Image.Rect` `[x, y, w, h]`, bottom-left origin | `Block.Loc.BBox`, displayed top-left space |
| `Image.Type`, `ColorSpace` | `Block.Attrs["inline"]`, `Attrs["color_space"]` |
| `Metadata.Title`, `Encrypted` | Same names |
| `Metadata.Author` | `Metadata.Authors`, one entry, not split |
| PDF `/Keywords` | `Metadata.Keywords`, one entry, not split |
| `Metadata.Creator` | `Metadata.Application` |
| `Metadata.Producer` | `Metadata.Extra["producer"]` |
| PDF bookmarks (not extracted today) | `Document.Outline` |
| `LoadPDF` / `LoadPDFConcurrent` | `gofastpdf.Extract(ctx, r, size, opts)` |

The PDF library needs a baseline layout stage (lines, paragraphs, column order) before its first release under this model, because of contract rules 2 and 3. Headings, lists, tables and header/footer tagging can follow incrementally without a schema change; until then every text block is a `paragraph` with `OriginInferred`.

## Example output

```json
{
  "schema_version": "0.1",
  "source": {"format": "pdf", "name": "report.pdf"},
  "metadata": {"title": "Q3 Report", "authors": ["A. Writer"]},
  "outline": [{"title": "Q3 Report", "level": 1, "part": 1, "y": 72}],
  "parts": [
    {
      "kind": "page", "number": 1, "width": 612, "height": 792,
      "blocks": [
        {"kind": "heading", "level": 1, "text": "Q3 Report",
         "loc": {"bbox": {"x0": 72, "y0": 72, "x1": 300, "y1": 96}}},
        {"kind": "paragraph", "origin": "inferred", "text": "Revenue grew in every region."},
        {"kind": "table", "origin": "inferred", "table": {"header_rows": 1,
         "rows": [["Region", "Rev"], ["EMEA", "1.2M"]]}}
      ]
    },
    {
      "kind": "page", "number": 2, "width": 612, "height": 792,
      "blocks": [],
      "warnings": [{"code": "part.image_only", "severity": "warning"}]
    }
  ],
  "warnings": [{"code": "input.repaired", "severity": "info", "message": "xref rebuilt"}]
}
```

## Deferred

- **Streaming.** No shared `Sink` in 0.1, but nothing prevents one: parts are self-contained, carry their own warnings, and need not be numbered contiguously. One conflict must be settled before a sink is designed. Some inferred structure needs the whole document (PDF heading levels need the document-wide body font size; header/footer tagging compares pages), so a streaming extractor either offers only page-local inference or runs a statistics pass first. That decides whether a sink receives final parts or provisional ones. When it is added, it should come with a serialized form (one header line, one line per part, one trailer line) for the CLI and non-Go consumers.
- **Numeric confidence.** Only if measurements from the PDF layout work show that a score would be meaningful.
- **Typed table cells.** Cells are strings. Typed values can be added alongside if a consumer needs them.
