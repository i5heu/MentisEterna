import MarkdownIt from "markdown-it";
import spoilerPlugin from "../spoilerPlugin.js";

describe("spoilerPlugin", () => {
    let md;

    beforeEach(() => {
        md = new MarkdownIt().use(spoilerPlugin);
    });

    test("renders a spoiler block with title and content", () => {
        const html = md.render(
            "::: spoiler Click to see the hidden secret!\nThis is the secret.\n:::",
        );
        expect(html).toContain('<div class="spoiler-wrapper">');
        expect(html).toContain('<details class="spoiler">');
        expect(html).toContain("<summary>Click to see the hidden secret!</summary>");
        expect(html).toContain('<div class="spoiler-content">');
        expect(html).toContain("<p>This is the secret.</p>");
    });

    test("escapes the title", () => {
        const html = md.render("::: spoiler <script>alert(1)</script>\nbody\n:::");
        expect(html).not.toContain("<script>");
        expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    });

    test("leaves non-spoiler blocks untouched", () => {
        const html = md.render("::: code\nx = 1\n:::");
        expect(html).toContain("x = 1");
        expect(html).not.toContain("spoiler");
    });

    test("renders markdown inside the spoiler body", () => {
        const html = md.render(
            "::: spoiler More\n**bold** and `code`\n:::\n",
        );
        expect(html).toContain("<strong>bold</strong>");
        expect(html).toContain("<code>code</code>");
    });

    test("a spoiler without a closing marker is rendered as plain text", () => {
        const html = md.render("::: spoiler Never closed\nsome text");
        expect(html).not.toContain('<details class="spoiler">');
        expect(html).not.toContain("spoiler-content");
        expect(html).toContain("<p>::: spoiler Never closed");
        expect(html).toContain("some text");
    });
});
