import { describe, expect, it } from "vitest";

import { snippet } from "./snippet.mjs";

const source = `package landing

func triage() error {
	// snippet:durable
	store, err := storefs.Sessions("sessions")
	if err != nil {
		return err
	}

	// snippet:inner
	if store == nil {
		fmt.Println(store)
	}
	// end
	if err := a.Close(); err != nil {
		return err
	}
	// end
	return nil
}
`;

describe("snippet", () => {
  it("returns the lines between the markers, without their indent", () => {
    expect(snippet(source, "inner")).toBe(
      ["if store == nil {", "\tfmt.Println(store)", "}"].join("\n"),
    );
  });

  it("drops each error check and the markers of an inner snippet", () => {
    expect(snippet(source, "durable")).toBe(
      [
        'store, err := storefs.Sessions("sessions")',
        "",
        "if store == nil {",
        "\tfmt.Println(store)",
        "}",
        "if err := a.Close(); err != nil {",
        "\treturn err",
        "}",
      ].join("\n"),
    );
  });

  it("fails for a snippet that the source does not have", () => {
    expect(() => snippet(source, "missing")).toThrow(/missing/);
  });
});
