import { expect, test } from "vitest";

import { supportedPlatforms } from "./projectScope";

test("only Windows and Linux x64 are in the initial platform scope", () => {
  expect(supportedPlatforms).toEqual(["windows-x64", "linux-x64"]);
});
