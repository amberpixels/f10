import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import { Notes } from "./notes.js";

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), "notes-"));

test("names are sorted", () => {
  const notes = new Notes(tmp());
  notes.save("b", "second");
  notes.save("a", "first");

  assert.deepEqual(notes.names(), ["a", "b"]);
});
