// Notes keeps text notes as <name>.txt files in one directory.
import fs from "node:fs";
import path from "node:path";

export class Notes {
  constructor(dir) {
    this.dir = dir;
  }

  save(name, text) {
    fs.writeFileSync(this.path(name), text);
  }

  // names lists the notes, sorted.
  names() {
    return fs
      .readdirSync(this.dir)
      .filter((f) => f.endsWith(".txt"))
      .map((f) => f.slice(0, -".txt".length))
      .sort();
  }

  path(name) {
    return path.join(this.dir, `${name}.txt`);
  }
}
