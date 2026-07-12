// markdown-it plugin for spoiler / accordion blocks
//
// Syntax:
//   ::: spoiler Click to see the hidden secret!
//   This content is safely hidden inside an accordion block!
//   :::
//
// Renders as a <details>/<summary> HTML element with full markdown support inside.

export default function spoilerPlugin(md) {
    const SPOILER_MARKER = ":::";
    const SPOILER_TAG = "spoiler";

    // Render: div.spoiler-wrapper > details.spoiler > summary + div.spoiler-content
    md.renderer.rules.spoiler_open = function (tokens, idx) {
        const title = md.utils.escapeHtml(tokens[idx].info);
        return '<div class="spoiler-wrapper">\n<details class="spoiler"><summary>' + title + '</summary>\n<div class="spoiler-content">\n';
    };

    // Render the closing tags
    md.renderer.rules.spoiler_close = function () {
        return '\n</div>\n</details>\n</div>\n';
    };

    // Block rule: parse ::: spoiler Title ... ::: containers
    function spoilerRule(state, startLine, endLine, silent) {
        const pos = state.bMarks[startLine] + state.tShift[startLine];
        const max = state.eMarks[startLine];
        const lineText = state.src.slice(pos, max);

        // Must start with ::: (our marker)
        if (!lineText.startsWith(SPOILER_MARKER)) return false;

        const afterMarker = lineText.slice(SPOILER_MARKER.length);

        // Skip whitespace after :::
        const trimmed = afterMarker.trimStart();
        if (!trimmed.startsWith(SPOILER_TAG)) {
            return false;
        }

        // Extract title (everything after "spoiler")
        const titleRaw = trimmed.slice(SPOILER_TAG.length).trim();

        // In silent mode, just report we can handle this
        if (silent) return true;

        // Find the matching closing :::
        let nextLine = startLine + 1;
        let foundEnd = false;
        for (; nextLine <= endLine; nextLine++) {
            const np = state.bMarks[nextLine] + state.tShift[nextLine];
            const nmax = state.eMarks[nextLine];
            const ntext = state.src.slice(np, nmax).trim();
            if (ntext === SPOILER_MARKER) {
                foundEnd = true;
                break;
            }
        }

        if (!foundEnd) return false;

        // Push open token
        const openToken = state.push('spoiler_open', 'details', 1);
        openToken.info = titleRaw;
        openToken.block = true;
        openToken.map = [startLine, nextLine + 1];

        // Let the normal block tokenizer process inner lines.
        // The inner content will be tokenized as paragraphs, lists, code fences, etc.,
        // and the spoiler_open / spoiler_close tokens wrap them.
        const oldParent = state.parentType;
        const oldLineMax = state.lineMax;
        state.parentType = 'spoiler';
        state.lineMax = nextLine;

        state.line = startLine + 1;
        state.md.block.tokenize(state, state.line, state.lineMax);

        state.parentType = oldParent;
        state.lineMax = oldLineMax;

        // Push close token
        const closeToken = state.push('spoiler_close', 'details', -1);
        closeToken.block = true;

        state.line = nextLine + 1;
        return true;
    }

    // Register before 'fence' so code fences take priority
    md.block.ruler.before('fence', 'spoiler', spoilerRule, {
        alt: ['paragraph', 'reference', 'blockquote', 'list'],
    });
}
