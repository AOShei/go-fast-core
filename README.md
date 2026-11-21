# go-fast-core

The shared document model and extractor interface for the go-fast-* extraction libraries. Standard library only.

Every extractor (`go-fast-pdf`, `go-fast-ooxml`, `go-fast-odf`, `go-fast-html`, `go-fast-rtf`, `go-fast-email`) returns the same `*gofastcore.Document`, so chunkers, indexers and knowledge-graph builders are written once and work for every format.

```go
import "github.com/AOShei/go-fast-core" // package gofastcore
```

**Status: module v0.1.1, schema version 0.2.** The module version is the Go module's tag; the schema version is the `schema_version` in every document and changes only as [Schema evolution](#schema-evolution) says. Schema 0.1 was agreed with the PDF extractor over two review rounds on 2026-11-18. Schema 0.2 (2026-11-20) replaced the hidden-text verdicts with what a file declares about display; see [What the file declares](#what-the-file-declares). Changes are in [CHANGELOG.md](CHANGELOG.md).

## Series conventions

- **Naming.** Every package carries the brand prefix: `gofastcore`, `gofastpdf`, `gofasthtml`. No package in the series shares a name with a standard library package, so callers never need an import alias.
- **Layout.** A library's public API is in its module root. Implementation goes under `internal/`, the CLI under `cmd/`. No `pkg/` directory.
- **Format-specific settings.** `Options` holds only what every format understands. Settings for one format (for PDF: memory mapping, a cache budget) are fields on that library's extractor value: `(&gofastpdf.Extractor{...}).Extract(...)`.
- **Dependencies.** `go-fast-core` imports only the standard library. A library's package imports only the standard library and `go-fast-core`, never another extractor; its command may import other go-fast extractors to hand on embedded documents (see [Embedded documents](#embedded-documents)). Any other dependency must be challenged.
- **Pinned versions.** A library requires `go-fast-core`, and a command other go-fast modules, at a tag or at the pseudo-version of a pushed commit. No `replace` directive or `go.work` file is committed.
- **Bounded input.** Every input is read within `Options.Limits`: what a file decides (its size, how much it decompresses to, how much text it becomes, how many parts and how deep its embedded documents go) is bounded. Reaching a bound truncates the document with a `limit.exceeded` warning, or is `ErrLimitExceeded` when there is no document yet. The same holds for the fixed bounds a library keeps of its own (the longest field instruction it reads, the most columns of a table): what they cut or leave out is reported with `limit.exceeded`, never dropped silently. Warnings are bounded too: see [Warnings](#warnings).
- **Nothing fetched or run.** The libraries never run what a file contains (macros, scripts, actions) and never fetch or send anything a file refers to. They report that it is there (`input.active_content`, `input.external_reference`).
- **Layering.** The shared model is the common denominator across formats. An extractor may offer a richer native API beside it (for PDF: word and line boxes, font names and sizes, glyph positions). Those details stay out of core.
- **Preserve content.** Anything that is real content, whether it carries meaning or provides context, is preserved where possible. Normalizing a representation is fine (a ligature to its letters, `\r\n` to `\n`); dropping or flattening something the author put there is not. This is why tabs are kept in text, running headers and footers are tagged instead of removed, and an attachment that cannot be extracted is still listed.
- **Report what the file declares; never judge.** Content the file says is not shown, or draws with no ink, is extracted like any other and carries that declaration. The libraries do not judge whether text is visible, suspicious or safe. See [What the file declares](#what-the-file-declares).
- **No OCR.** The go-fast libraries extract what the file contains. They report what they found and leave the next step to the caller.

## The model

```
Document
├── Source      format, name, MIME, size
├── Metadata    title, authors, dates, language, extra
├── Outline[]   the source's own table of contents (PDF bookmarks, epub nav)
├── Parts[]     the format's natural unit: page, slide, sheet, chapter, message, body;
│   │           and, last, what the file stores and shows nowhere (stored)
│   ├── Blocks[]    content in reading order
│   └── Warnings[]  what went wrong in this part
├── Children[]  embedded documents (attachments, archive members), each a full Document
└── Warnings[]  what went wrong for the document as a whole
```

A block has a `Kind`, usually `Text`, and kind-specific fields:

| Kind | Text | Level | Other fields |
|---|---|---|---|
| `heading` | heading text | 1–6 | `Marker` for a numbered heading; `Attrs["level"]` if the source level is deeper than 6 |
| `paragraph` | paragraph text | | |
| `list_item` | item text without the marker | nesting depth, from 1 | `Ordered`, `Marker` |
| `table` | empty | | `Table` (rows of cell strings, header row count, merges, details of cells); `Attrs`: `name`, `role` (`chart`, `pivot`), for a chart `chart`, `x_axis`, `y_axis` |
| `image` | alt text or description | | `Image` (id, MIME, pixel size, optional bytes) |
| `code` | verbatim code | | `Attrs["language"]` |
| `quote` | quoted text | nesting depth, from 1 | |
| `caption` | caption text | | |
| `footnote` | note text | | `Marker` |
| `formula` | LaTeX, UnicodeMath or plain text | | `Attrs["notation"]`: `latex`, `unicodemath`, `plain` |
| `note` | speaker note, comment, text removed by a tracked change, field instruction, or the values of a drop-down list | | `Attrs`: `type`, `author`, `date`, `field` |
| `header`, `footer` | running page header/footer | | |
| `thematic_break` | empty | | |

A `thematic_break` is a break between topics or scenes that the source declares: an HTML `<hr>`, a Markdown `---`, Word's horizontal line, an empty paragraph in LibreOffice's Horizontal Line paragraph style (`Horizontal_20_Line`, which is how LibreOffice writes an `<hr>`). A named style counts as a declaration, as a heading style does. It is always `native`: a bordered or ruled paragraph, a drawn line or a row of asterisks is formatting, not a declared break, and no break is inferred from it (`Validate` rejects an `inferred` one). It is a boundary a chunker can split on. It has no text, is never `Continued`, and `Markdown()` writes it as `---`; `Text()` writes nothing for it, since blocks are already separated by a blank line. Page, column and section breaks are layout, not content, and are not emitted as blocks: a paged format already has its pages as parts.

Every block may also carry:

- `Origin`: `native` when the source declares the block's kind, level and boundaries (a docx style, an HTML tag, a tagged-PDF element, a PDF outline entry), `inferred` when the extractor deduced them from geometry or fonts. Absent in JSON means `native`. It does not cover the block's position in the reading order: a PDF image is `native` even though where it falls among the text is inferred.
- `Display`: the reasons the file declares that content in this block is not shown (`hidden`) or drawn with no ink (`no_ink`). Absent in JSON means the file declares neither, which says nothing about whether a reader can see the text. See [What the file declares](#what-the-file-declares).
- `Continued`: this block continues the nearest block of the same kind before it: the last one of the previous part, such as a paragraph split by a page break, or the one before it in the same part, such as a table too long for one block and split by rows. A continued table has the columns of the table it continues. The renderers run a continued block on to the one it continues.
- `Loc`: where it came from. `BBox` for paged formats, `Range` (A1 notation) for sheets, `Offset`/`Length` for plain-text formats, `Anchor` for formats where the source itself names its blocks (a Word paragraph id, an HTML `id`, an e-book fragment). This is what lets a retrieval hit be cited.
- `Spans`: inline bold/italic/link ranges over `Text` (optional), and the ranges that carry a `Display` reason (always present when the reason applies to only part of a block).
- `Attrs`: format-specific key/values with no dedicated field.

### Labels, notes and deep headings

- **`Marker` is the label a reader sees in front of a block**: the label of a list item (`3.`, `(a)`, `4.2.1`), the number of a numbered heading (`2.3`), the mark of a footnote. It is content (a contract refers to "clause 4.2"), it is kept out of `Text`, and `Markdown()` and `Text()` put it back in front: `## 2.3 Scope`.
- **`note` blocks say what they are in `Attrs`**, under keys every format spells the same way (constants `AttrNoteType`, `AttrAuthor`, `AttrDate`):

  | `type` | Meaning |
  |---|---|
  | `comment` | A reviewer's comment or annotation attached to the content |
  | `deletion` | Text removed by a tracked change. The document's text is given as if the change were accepted; the removed text is kept here |
  | `speaker` | The speaker's notes of a slide |
  | `field` | The instruction of a field whose result is in the text (`HYPERLINK ...`, `MERGEFIELD Name`). It is never shown, but that is what a field is, not a declaration: no `Display` reason. It is stored data: always marked `stored=field_code`, so the renderers leave it out unless asked |
  | `attribute` | Text a markup element keeps in an attribute rather than in its content (`aria-label`, `title`, `placeholder`, an image map area's `alt`), at the element's place, with the attribute's name in `attribute`. No `Display` reason |
  | `headers` | The headers of a message that a mail program does not show, one `Name: value` per line. No `Display` reason |
  | `mime_preamble`, `mime_epilogue` | The text of a multipart message body before its first part and after its last, which no mail program shows. No `Display` reason |
  | `choices` | The values a control offers to choose from (a spreadsheet cell's drop-down, a drop-down form field or content control, a PDF choice field), one per line in the source's order. A list taken from cells elsewhere is given as the reference to them (`=Lists!$A$1:$A$12`), since those values are already in the text. It follows the control's block, or on a sheet stands at the cells it is for (`Loc.Range`), with the control's name in `field` where the source names one. No `Display` reason |

  `author` and `date` are as the source gives them.
- **The values of `type` (on a note), `role` and `stored` are closed sets**, the `Note*`, `Role*` and `Stored*` constants: every format means the same by each, so a consumer can filter on them across formats. A library writes no other value, and `Validate` rejects one. A value a library needs is added to core; a fact only one format has goes under a key of that library's own. `role` says what a block stands for when its kind alone does not: `chart` and `pivot` on a table, `headers` on the block of a message's headers a mail program shows, `signature` on a block of a message's signature.
- **A table's cells can say more than their text** (`Table.Cells`, an entry only for a cell that has something): where the cell links to (`href`, with `Spans`), the formula whose result it shows (`formula`, never evaluated), and the type of the value it stores with the value where the text does not show it in full (`type`: `number`, `date`, `bool`, `error`, `text`; `value`), both with `Options.Cells`. The renderers write the links, never formulas or values: a formula can hold text nobody sees on the sheet.
- **Data the file stores and never shows** is read as data, never run, evaluated or followed, with `Attrs["stored"]` saying what it is: `pivot_cache` (the records of a pivot table whose source is gone), `defined_name`, `document_variable`, `custom_xml`, `field_code` (the instruction of a field: every `field` note carries it, and `Validate` checks that), `macro`, `markup_comment` (an HTML or XML `<!-- -->` comment, which no reader displays; it stays where it stands; a reviewer's comment is a `comment` note and is not stored), `form_field` (a field of a form shown on no page), `after_end` (data after the logical end of the file: after an RTF document's closing brace, after a PDF's last `%%EOF`, appended to a zip; the part also carries an `input.trailing_data` warning), `manifest_item` (a resource a publication lists and no chapter shows, such as an EPUB's unused image), `stylesheet` (the text of a style sheet, an HTML `<style>`), `generator` (the program that wrote the file, where the file records it as data), `custom_property` (a property the author or a program defined, with its name in `name`). `StoredKindNames()` lists them. Being stored records where the text came from; it sets no `Display` reason. What belongs to the file and to no page, sheet or slide goes in a part of kind `stored`: at most one, the last, with no part-level reason. Stored data that belongs to a page, sheet or slide (the scripts of a PDF page) stays in that part, marked with `Attrs["stored"]`, so that its place is kept. The renderers leave out the stored part, and every block marked stored wherever it is, unless `RenderOptions.Stored` is set; `RenderOptions.StoredKinds` includes only the kinds it names (for example only `markup_comment`), in the stored part and elsewhere. To collect all stored data, check `Attrs["stored"]` on every block, not only the stored part. `Options.Parts` selects it only with the whole document, and a reader that gives parts one at a time gives it last.
- **The value of a form field or control** that its own appearance does not show (a PDF form field, a Word form field or content control) is a block at its place, with the field's name in `Attrs["field"]` (`AttrField`).
- **Heading levels are 1 to 6.** A source with deeper levels (Word has nine) has them clamped to 6, with the real level in `Attrs["level"]`, and the part carries the `structure.heading_level_clamped` warning at severity `info`. Levels above six are not allowed because Markdown has none.

### Design decisions

- **Flat blocks, no section tree.** Hierarchy is implied by heading levels. Extractors emit in one pass and page workers stay independent.
- **Parts are self-contained.** A part owns its blocks and its warnings. A page worker fills one `Part` with no shared state and no locking, and a part handed on by itself is complete.
- **Structure first, Markdown derived.** `Markdown()` and `Text()` render the blocks. Extractors never emit Markdown strings. `Markdown()` writes links, of text and of table cells, as `[text](<address>)`; other inline formatting is not rendered, and `Text()` has no links.
- **Provenance, not confidence.** `Origin` says whether structure was declared or guessed. There is no numeric score: heuristics do not produce calibrated probabilities. A consumer that needs certainty treats `inferred` blocks as plain text and chunks by size.
- **Tables are `[][]string`.** Merged cells put their text top-left and are listed in `Merges`. Numbers appear as the source displays them: thousands separators, decimals, percent and currency signs as formatted.
- **Dates and times stored as values are written in ISO 8601**, whatever the display format: `2026-03-04`, `2026-03-04T14:30:00`, `14:30:00`. This is the one exception to "as displayed", and it applies where the source holds a date as a value with a display format beside it (a spreadsheet cell), not to dates written as text. The reason is that `03/04/2026` means two different days depending on where the file was made, and the file does not always say. The exception standardises and loses nothing: every component the value holds is kept, so a cell with a time of day keeps it, and a fraction of a second is kept where there is one.
- **Furniture is kept but tagged.** Running headers and footers are `header`/`footer` blocks; the renderers drop them by default.
- **Partial results beat errors.** A document that is 95% readable returns with warnings. An error means nothing usable came out.

## Extractor contract

1. All strings are valid UTF-8 and follow the text conventions below.
2. A block is at least a paragraph-sized unit, never a fragment of a line or word.
3. Blocks are in reading order. Where the format does not declare order (PDF), order is the extractor's best inference.
4. A top-level document has at least one part.
5. `Part.Number` is the part's 1-based ordinal in the source. Numbers strictly increase but need not be contiguous: extracting pages 40–60 yields parts numbered 40 to 60.
6. A block that runs across a part boundary is either assigned to the part where it starts or split, with `Continued` set on the later piece. A table too large for one block is split by rows within its part, with `Continued` set on the later pieces.
7. `Table` is set exactly on `table` blocks, `Image` exactly on `image` blocks.
8. `Extract` is safe to call from many goroutines and honors context cancellation.
9. Content the file declares not shown or not inked is extracted like any other and carries the reason in `Display`, never dropped. Nothing else about visibility is judged. Every extractor calls `part.Finish()` on each finished part.
10. `Document.Parts`, `Part.Blocks` and `Table.Rows` are never nil, so JSON has `[]` and never `null`. A table has at least one row and one column.
11. Output is deterministic: the same input and options give the same document whatever `Options.Workers` is, including where a limit truncates it.

`doc.Validate()` checks everything mechanical in rules 1, 4, 5, 7 and 10, and in rule 9 that `part.Finish()` was called, plus spans, tables, merges, warnings, enum values and the closed sets of `stored`, `type` and `role`. Run it on every test fixture and fuzz output:

```go
doc, err := gofastpdf.Extract(ctx, f, size, nil)
if err != nil { t.Fatal(err) }
if err := doc.Validate(); err != nil { t.Fatal(err) }
```

### Text conventions

- Text is in logical order, the order in which it is read and typed, also for right-to-left scripts. An extractor that cannot guarantee this for some input says so with a warning.
- Soft line breaks inside a paragraph are joined with a single space. A `\n` in `Text` means a hard break.
- End-of-line hyphenation is resolved only when the extractor is certain. Otherwise the hyphen stays and the lines are joined with no space: `inter-` + `national` gives `inter-national`, so a real compound such as `well-` + `known` comes out right.
- **Ligature characters are written as their letters, everywhere.** The seven presentation-form ligatures U+FB00 to U+FB06 come out as `ff`, `fi`, `fl`, `ffi`, `ffl`, `st`, `st` in every piece of text a library extracts: block text, table cells, markers, part names, outline titles, alt text, and the title, subject, authors and keywords in the metadata. The output is for retrieval, and a search for "office" must find a document that spells it with U+FB01. Nothing wider is done: this is not compatibility normalization, and no other character is rewritten. Strings that identify or address something are left as the source has them: a link target, an image ID, an anchor, the source's file name. Use `Ligature`, `ExpandLigatures` or `AppendLigatures` from this package; no library keeps its own table.
- No string contains a control character (NUL, form feed, carriage return, ...) other than `\n` and `\t`.
- Block text is not empty and has no leading or trailing whitespace.
- Code blocks that the source declares as code are verbatim: the ligature and whitespace rules do not apply to them, because there the character is what the author typed. Code that an extractor infers from layout (PDF) has no such claim, and its ligatures are expanded like any other text.
- Unicode normalization form is not guaranteed (normalization is not in the standard library).

`Validate` enforces the rules it can check mechanically: no control characters in any string, no unexpanded ligatures in any of the text listed above (code blocks excepted), and trimmed non-empty block text.

### JSON

`parts`, `blocks` and `rows` are always arrays, `[]` when empty and never `null`, so a consumer can iterate them without a nil check on a failed page or an unextracted child.

The model has no custom JSON marshalers, which keeps encoding at the full speed of `encoding/json`. The guarantee comes from the lists never being nil: `New` creates an empty parts list, `part.Finish()` makes an empty blocks list, and a table has at least one row and one column. `Validate` rejects a nil list.

### Coordinates

`Part.Width`/`Height` and `Loc.BBox` describe the part **as displayed**: origin top-left, y increasing downward. For PDF that is the CropBox after `/Rotate` and `UserUnit` are applied, in points. A box drawn at `BBox` on a rendered image of the page encloses the block.

### Span offsets are bytes

`Span.Start` and `Span.End` are **byte offsets into the UTF-8 text**, not character offsets. They always fall on character boundaries. Languages that index strings by code point must convert. In Python:

```python
text.encode("utf-8")[span["start"]:span["end"]].decode("utf-8")
```

### Images

Every image is a block unless the caller asks for none (`ImagesNone`): where a picture stands is context, its alternative text is text its author wrote, and a caller may want to look at the picture itself (`ImagesData`). `Image.ID` identifies the image resource, not the block. Equal IDs mean equal bytes, and several blocks may share one ID (a logo on every page). With `ImagesData`, the bytes are guaranteed only on the first block with a given ID in document order; repeats may omit them.

## Warnings

A warning is a fact about what happened. It never recommends an action.

```json
{"code": "part.image_only", "severity": "warning", "message": "1 image, no text operators"}
```

Warnings local to a part are in `Part.Warnings`; those about the whole file are in `Document.Warnings`. `doc.AllWarnings()` iterates both, and `part.Has(code)` tests one part.

A warning that can be raised many times in one part (once per code page, per charset label, per chapter) is raised once, with a count. As a bound for the rest, `Part.Warn` and `Document.Warn` keep at most `MaxWarnings` (1000) warnings on a part or a document; past it they count the rest in one last `warnings.omitted` warning, whose severity is the highest of those it counts.

| Severity | Meaning |
|---|---|
| `info` | Something notable happened; the output is complete |
| `warning` | The output is present but incomplete or degraded, or it is one of more than one way the input can be read |
| `error` | Content is missing or untrustworthy |

Shared codes (constants `Warn*`):

| Code | What happened |
|---|---|
| `part.failed` | The part could not be parsed; its blocks are missing or incomplete |
| `part.no_text` | The part was parsed and contains no text and no images |
| `part.image_only` | The part was parsed and contains no text, but does contain image content. Reported whatever `Options.Images` is set to |
| `text.tracked_changes` | The part has tracked changes. Its text is given as if all were accepted; each deletion is kept as a `note` block of type `deletion` |
| `structure.heading_level_clamped` | The part has headings deeper than six levels; each has level 6 and its real level in `Attrs["level"]` |
| `text.unmapped_glyphs` | Some text has no Unicode mapping in the source and appears as U+FFFD or is absent |
| `image.unsupported_encoding` | Image data was requested and an image uses an encoding the extractor cannot deliver; its block has no data |
| `input.repaired` | The input was structurally damaged and was read by a recovery path |
| `input.trailing_data` | Data follows the logical end of the file; its text is in the stored part with `Attrs["stored"]` `after_end`. The message says how many bytes follow the end |
| `input.active_content` | The input contains something meant to be run, or an instruction to run something: macros, scripts, an action that starts a program or opens a file. It is not read and never run |
| `input.external_reference` | The input refers to a file or address outside itself other than by an ordinary hyperlink: a remote template, a linked picture, an action that sends or loads data. Nothing is fetched or sent |
| `input.ambiguous` | The input can be read as two different documents depending on the program, such as an archive that names a member twice in two ways |
| `limit.exceeded` | A limit was reached during extraction, one of `Options.Limits` or a fixed bound of the extractor; the output is cut or left out there, and the message says what |
| `warnings.omitted` | More than `MaxWarnings` warnings were raised on the part or document; the rest are counted here, and the message begins with their number. Added by `Warn` at the end of the list; warnings an extractor appends to the list directly can follow it |
| `child.not_extracted` | An embedded document is listed but was not extracted |
| `child.unsupported` | No extractor handles the embedded document's format, or it uses an unimplemented feature |
| `child.encrypted` | The embedded document is encrypted and was not opened (no password, the wrong one, or no key for the certificate it is encrypted for) |
| `child.failed` | Extraction of an embedded document failed for any other reason |

An extractor may add codes of its own under a format prefix, such as `pdf.font_missing`.

This is how a caller handles scanned pages without go-fast knowing anything about OCR:

```go
for i := range doc.Parts {
    p := &doc.Parts[i]
    if p.Has(gofastcore.WarnPartImageOnly) {
        sendToMyOCR(path, p.Number) // the caller's decision, not ours
    }
}
```

## What the file declares

The libraries report what a file declares about its content; they do not judge whether text is visible or harmful, and the absence of a reason says nothing about either. Threat detection is outside these libraries.

Everything a file contains is extracted, including content the file says is not to be shown. Where the file declares that, the block, span, table region or part carries the reason in `Display`:

| Reason | The file declares |
|---|---|
| `hidden` | The content is switched off: hidden formatting or the hidden attribute; a hidden sheet, row, column, slide, shape, layer or annotation; `display:none` |
| `no_ink` | The text is drawn with no paint: no fill, full transparency, PDF text render mode 3 or 7 |

**What counts as declared.** A reason is set only where the file states it, in one switch or one value of the content or of something containing it. Following the file's own inheritance (a style, an ancestor element) to find that switch counts. If deciding needs **two values compared** or **a threshold**, it is not a declaration and is not reported: colour against background (white on white included), a size against a minimum, a box against the page or against another box, the coverage of an image, an evaluated condition. Each library's README lists, one line each, the declarations it reports for its format.

**Characters are kept.** Zero-width characters, Unicode tag characters, bidirectional controls and variation selectors stay in the text as the file has them, subject only to the [text conventions](#text-conventions). No reason or warning is set for them.

**Scanned PDFs.** The searchable text layer of a scanned page is drawn with no ink, so it carries `no_ink`. Excluding `no_ink` leaves that text out.

### Where the reason is

- **`Block.Display`** lists every reason declared anywhere in the block, so `b.Display != 0` is the only test needed.
- **Part of a block.** When a reason applies to only a passage, a `Span` with `Display` set marks the range; for a table, a `DisplayCells` region marks the cells. A reason that appears on a span or region applies only there; one that appears on none applies to the whole block. `b.DisplayWhole()` and `b.DisplayedText(exclude)` do this arithmetic.
- **`Part.Display`** marks a whole hidden sheet or slide; its blocks carry the same reasons.
- There is no warning for content that carries a reason. Warnings are for facts about the extraction: damage, limits, unsupported features, encryption, trailing data.

```json
{"kind": "paragraph", "display": "hidden",
 "text": "Shown one. Hidden sentence. Shown two.",
 "spans": [{"start": 11, "end": 27, "display": "hidden"}]}
```

A reason added by a newer extractor decodes as `unknown` and is kept.

### Using it

`Markdown()` and `Text()` include everything by default. To leave declared content out of rendered output:

```go
// Everything with a declared reason:
doc.WriteText(w, &gofastcore.RenderOptions{ExcludeDisplay: gofastcore.DisplayAny})

// Hidden content only, keeping the no-ink text layer of scanned pages:
doc.WriteText(w, &gofastcore.RenderOptions{ExcludeDisplay: gofastcore.DisplayHidden})
```

Exclusion cuts exactly the ranges that carry the reason.

### For extractor authors

1. Set a reason only where your format declares it (the rule above), on the block, span, table region or part. List those declarations in your README, and say plainly that colour, size, position and overlap are not judged.
2. Never merge declared content into another block without marking the range.
3. Call `part.Finish()` once per finished part, including a failed or blank one. It copies `Part.Display` to the blocks and makes an empty blocks list non-nil. `Validate` fails if it was skipped.
4. Don't remove or flag invisible characters, and never describe output as safe, suspicious or malicious.

## Extractor interface

```go
type Extractor interface {
    Formats() []string
    Extract(ctx context.Context, r io.ReaderAt, size int64, opts *Options) (*Document, error)
}
```

`Options` (zero value: text, structure and a block for every image, whole input, all CPUs):

| Field | Purpose |
|---|---|
| `Workers` | Goroutines inside one document; 0 means all CPUs |
| `Images` | `ImagesMeta`, the default: a block for every image, with its place, size and alternative text. `ImagesData`: the bytes too. `ImagesNone`: no image blocks, and so no alternative text |
| `Spans` | Ask for inline formatting and links |
| `Password` | Tried on encrypted input, and passed down to embedded documents |
| `Keys` | Private keys (`crypto.Decrypter`) tried on input encrypted for a certificate: the one whose public half matches a recipient the file names. Passed down like `Password` |
| `Cells` | Ask for the formulas, types and stored values of table cells in `Table.Cells` |
| `Parts` | `PartRange{First, Last}`, inclusive part numbers; zero value means all; a range that selects nothing is `ErrNoPartsSelected` |
| `Limits` | `MaxInputBytes`, `MaxDecodedBytes`, `MaxTextBytes`, `MaxParts`, `MaxDepth`. Zero means the default; negative means no limit |
| `Child` | Function that extracts embedded documents; see below |
| `Name` | Recorded as `Source.Name` |

### Errors

Extractors wrap these, so test with `errors.Is`: `ErrPasswordRequired`, `ErrWrongPassword`, `ErrKeyRequired`, `ErrUnsupported`, `ErrMalformed`, `ErrLimitExceeded`, `ErrNoPartsSelected`.

`ErrLimitExceeded` means a limit stopped the extraction before there was anything to return. That is always so for `MaxInputBytes`, and it is so for any other limit reached before the input could even be identified (a decompression bomb in the part that says what the file is). A limit reached once there is a document truncates it and adds a `limit.exceeded` warning with severity `error`; an error is never returned together with a document. `MaxParts` returns the first `MaxParts` parts of the requested range, also where the part count is known up front.

### How much text a file may become

A file can be small and say a great deal: one long string that every cell of a sheet refers to, a form drawn on every page of a PDF. What is repeated is stored once, so `MaxDecodedBytes` does not see it, and the document that results writes the text out as often as it occurs. A workbook of 110 KB can become 2 GiB of text this way.

`Limits.MaxTextBytes` bounds it: the text of blocks and the cells of tables, counted as often as they occur, in a document and the documents embedded in it together. The default is 256 MiB (`DefaultMaxTextBytes`). A part that reaches it is cut off there with `limit.exceeded`.

Every block takes `BlockCost` (128) bytes from the budget beyond its text, and every cell of a table `CellCost` (16) beyond its text, empty cells included: a file decides how many blocks and cells there are as freely as it decides how long a string is. A PDF page that draws one image 100,000 times needs six bytes of content for each; the 300,000 image blocks of 1.8 MB of content held 189 MB. `BlockCost` is about what a block takes written down with no text; `CellCost` is what a cell takes in memory. At the default limit a document has at most two million blocks.

Text outside blocks counts in the same way wherever the file decides both how long it is and how often it occurs: the titles of the outline, a part name that is put together for every part (a PDF page label with its prefix), list markers, link targets, the details of table cells. An extractor may instead hold such a string to the length of a name and leave it uncounted: the number of blocks and parts, which the other limits bound, then bounds it. Metadata occurs once and is not counted.

The outline is counted before the first part, and a part's name with the part, so that where a document is cut does not depend on the number of workers. When the budget is spent in the outline, the entries read so far are kept and the document carries `limit.exceeded`.

An extractor counts with a `TextBudget`:

```go
ctx, budget := gofastcore.WithTextBudget(ctx, opts.Limits)
defer budget.Close()
...
if !budget.Take(gofastcore.BlockCost + len(text)) {
    // keep what there is, warn limit.exceeded on the part, stop reading
}
...
opts.ExtractChild(ctx, name, mime, reader, size) // this ctx: the child shares the budget
```

The context carries the budget to the extractor of an embedded document, of whatever library: its `WithTextBudget` gives it no more than its parent has left, and its `Close` takes what it used from its parent. Embedded documents are extracted one after another, so the result does not depend on timing. An extractor that reads a document again from the start, to cut it at the same place whatever the number of workers, calls `Reset` first. One that has stopped at the limit by a count of its own (an outline already cut there) calls `Exhaust`, so that nothing is left for what follows.

The limit on decompressed bytes is shared in the same way, for the same reason turned around: each document's own count stops a bomb, and a document that embeds thousands of bombs, each just inside the limit, would cost thousands of limits. `WithDecodeBudget(ctx, opts.Limits, default)` gives the budget for one reading; before an embedded document is handed to `ExtractChild` the extractor sees to it that the budget says what the document has left (`Take` as it goes, or `Settle(left)` from a count of its own), and the child, reading within what it is given, takes what it used from its parent on `Close`.

### Embedded documents

An email attachment may be a PDF; a PDF may embed a spreadsheet. Extractors do not import each other. The caller injects a dispatcher as `Options.Child`, and an extractor hands every embedded file to `opts.ExtractChild(...)`, appending the result to `Children`:

```go
doc.Children = append(doc.Children, opts.ExtractChild(ctx, name, mime, reader, size))
```

`ExtractChild` always returns a document, so every embedded file is at least listed with its name and size. When the child was not extracted, the result has no parts and one warning whose code says why: `child.not_extracted` (no `Child` supplied, or depth limit reached), `child.unsupported`, `child.encrypted` or `child.failed`. The last three are chosen from the error the child extractor returned, so a dispatcher should return `ErrUnsupported` for formats it has no extractor for.

The renderers leave children out unless `RenderOptions.Children` is set. Then each child follows the document's own parts, depth first, after a line that names it (`<!-- child 2: report.pdf -->` in Markdown, `[child 2: report.pdf]` in text; a nested child is `child 2.1`), and the same options apply to it. JSON always has the children.

A library never imports another extractor. A command (`cmd/gofast<lib>`) may, to build its `Child` dispatcher: go-fast-email's command hands HTML and RTF bodies to go-fast-html and go-fast-rtf. The library's package still imports only core and the standard library; the module's `go.mod` lists the other extractors because its command needs them.

### Commands

Every command offers the same flags for `RenderOptions`, with the same names, values and meaning: `-furniture`, `-stored[=kinds]`, `-part-markers`, `-exclude-display[=reasons]` and `-children` (on by default). A command defines them with `AddRenderFlags` and sets its options with `Apply`, so that the list of kinds and reasons, and the parsing, are core's:

```go
render := gofastcore.AddRenderFlags(flags)
flags.Parse(args)
ro := &gofastcore.RenderOptions{}
render.Apply(ro)
```

## Schema evolution

`schema_version` changes when a field is removed or its meaning changes. Additions do not change it, including new block kinds: a decoder that meets a kind, part kind, origin or severity it does not know maps it to that enum's `Unknown` value, and unknown style names are ignored. An unknown block renders as a paragraph. Re-encoding such a document writes `"unknown"`, so the original name is lost. `Validate` rejects `Unknown`, since extractors must never emit it.

## How each format maps

| Format (library) | Part kind | Notes |
|---|---|---|
| pdf (go-fast-pdf) | `page` | `BBox` on blocks; structure is `inferred` unless the PDF is tagged or the outline confirms a heading; `Loc.Anchor` from named destinations and the `/ID` of structure elements |
| docx (go-fast-ooxml), odt and fodt (go-fast-odf), rtf (go-fast-rtf) | one `body` | Heading styles and outline levels give levels; numbered headings and list items carry `Marker`; comments, tracked deletions, field instructions and the values of drop-down controls become `note`; `Loc.Anchor` is the paragraph id (docx, odt) or the bookmark that starts the paragraph (rtf) |
| pptx (go-fast-ooxml), odp and fodp (go-fast-odf) | `slide` | `Name` is the slide title; speaker notes become `note` |
| xlsx (go-fast-ooxml), ods and fods (go-fast-odf) | `sheet` | `Name` is the sheet name; each used region is a `table` with `Loc.Range`; drop-down lists become `note` blocks of type `choices`; defined names and macro sheets go to the `stored` part |
| html, md, txt (go-fast-html) | one `body` | `Loc.Offset`/`Length` into the source; `Loc.Anchor` from `id` attributes |
| epub (go-fast-html) | `chapter` | One per spine item; nav becomes `Outline` |
| eml, msg, mbox, tnef (go-fast-email) | `message` | One part per message. For a single message every header is in `Metadata.Extra`; in a mailbox the headers a mail program does not show are a `note` of type `headers`. Attachments in `Children` |

go-fast-ooxml reads the macro and template variants of docx, xlsx and pptx, and binary workbooks (`.xlsb`), under those three format names. Each library's README gives the details. go-fast-pdf's MIGRATION.md maps its API before the shared model to this one.

## Example output

```json
{
  "schema_version": "0.2",
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
      "blocks": [
        {"kind": "image", "loc": {"bbox": {"x0": 0, "y0": 0, "x1": 612, "y1": 792}},
         "image": {"id": "obj-14", "mime": "image/jpeg", "width": 2550, "height": 3300}}
      ],
      "warnings": [{"code": "part.image_only", "severity": "warning", "message": "1 image, no text operators"}]
    }
  ],
  "warnings": [{"code": "input.repaired", "severity": "info", "message": "xref rebuilt"}]
}
```

## Deferred

- **Streaming.** No shared `Sink` in module v0.1.0 or schema 0.2, but nothing prevents one: parts are self-contained, carry their own warnings, and need not be numbered contiguously. One conflict must be settled before a sink is designed. Some inferred structure needs the whole document (PDF heading levels need the document-wide body font size; header/footer tagging compares pages), so a streaming extractor either offers only page-local inference or runs a statistics pass first. That decides whether a sink receives final parts or provisional ones. When it is added, it should come with a serialized form (one header line, one line per part, one trailer line) for the CLI and non-Go consumers.
- **Numeric confidence.** Only if measurements from the PDF layout work show that a score would be meaningful.
