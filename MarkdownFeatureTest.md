# Markdown Feature Test

This file exercises every markdown-it feature configured in the app:
- **Base**: heads, emphasis, code, links, images, lists, blockquotes, tables, hr, breaks, autolinks
- **footnote plugin**: inline refs + definitions
- **spoiler plugin**: `::: spoiler Title ... :::` accordion blocks

---

## Headings

### H3 level

#### H4 level

##### H5 level

###### H6 level

---

## Inline formatting

**bold text** • __also bold__ • *italic text* • _also italic_ • ***bold italic*** • ~~strikethrough~~ • `inline code`

---

## Links & images

[External link](https://example.com)  
Autolink: https://www.example.com  
![Placeholder](https://placehold.co/600x200/1a1a2e/6d9484?text=Test+Image)

---

## Lists

### Unordered
- Item one
- Item two
  - Nested item A
  - Nested item B
- Item three

### Ordered
1. First
2. Second
   1. Nested ordered
   2. Another nested
3. Third

### Task list
- [x] Completed task
- [ ] Pending task
- [ ] Another pending

---

## Blockquote

> This is a blockquote.
>
> It can span multiple paragraphs.
>
> > Nested blockquote level.

---

## Code blocks

```js
function greet(name) {
    console.log(`Hello, ${name}!`);
}
```

```bash
echo "Shell command"
ls -la
```

---

## Tables

| Feature   | Status | Notes           |
|-----------|--------|-----------------|
| Headings  | ✓      | All levels      |
| Inline    | ✓      | Bold, italic, etc. |
| Links     | ✓      | Internal + external |
| Code      | ✓      | Inline + fenced |

---

## Horizontal rule

Above the rule.

---

Below the rule.

---

## Line breaks (breaks: true)

This line ends with a newline character
and this continues on the next line without a blank line between them — it should render as a soft break (single `<br>`) because `breaks: true`.

This paragraph is separated by a blank line so it's a new `<p>`.

---

## Footnotes

Here is a footnote reference[^1].

Another one with more context[^longnote].

[^1]: This is the first footnote — it appears at the bottom.

[^longnote]: This footnote has **bold text**, a [link](https://example.com), and `code` inside it.

---

## Spoiler / accordion

::: spoiler Click to see the hidden secret!
This content is safely hidden inside an accordion block! 🤫✨
:::

::: spoiler Spoiler with rich content
# Full markdown inside!

You can put **bold**, *italic*, `code`, [links](https://example.com), and more.

- List item 1
- List item 2

```js
const secret = 'revealed';
```

> Blockquote inside a spoiler!

| Key | Value |
|-----|-------|
| A   | 1     |
| B   | 2     |
:::

---

## All features combined in a single spoiler

::: spoiler The ultimate test
## Headings work

**Bold** and *italic* and `inline code`.

- Lists
  1. Nested
  2. Ordered

> Blockquote inside

```
code block inside
```

[Link inside](https://example.com) and https://autolink.example.com

![Image inside](https://placehold.co/400x100/1a1a2e/6d9484?text=Inside+Spoiler)

Footnote inside[^inside] works too!

| Col 1 | Col 2 |
|-------|-------|
| a     | b     |

---

Horizontal rule inside — works!

Last line.
:::

[^inside]: Footnote inside a spoiler block.
