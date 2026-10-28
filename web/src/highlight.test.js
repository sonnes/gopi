import { describe, expect, it } from "vitest";

import { highlight } from "./highlight.mjs";

describe("highlight", () => {
  it("marks a comment and a string, and leaves the rest as ink", () => {
    expect(highlight('a := f("x") // why')).toBe(
      'a := f(<span class="st">"x"</span>) <span class="cm">// why</span>',
    );
  });

  it("does not start a comment inside a string", () => {
    expect(highlight('u := "http://x"')).toBe('u := <span class="st">"http://x"</span>');
  });

  it("keeps an escaped quote inside its string", () => {
    expect(highlight('s := "a\\"b"')).toBe('s := <span class="st">"a\\"b"</span>');
  });

  it("marks a raw string", () => {
    expect(highlight("t := `json:\"city\"`")).toBe('t := <span class="st">`json:"city"`</span>');
  });

  it("escapes the characters of HTML", () => {
    expect(highlight("if a < b && c > d {")).toBe("if a &lt; b &amp;&amp; c &gt; d {");
  });
});
