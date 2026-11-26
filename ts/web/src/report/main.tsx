import { render } from "preact";

import { RunPage } from "./RunPage";
import { SetPage } from "./SetPage";
import type { Payload } from "./types";
import "./report.css";

const payload = JSON.parse(document.getElementById("f10-page")!.textContent ?? "{}") as Payload;

render(
  payload.kind === "set" ? <SetPage set={payload.set} /> : <RunPage run={payload.run} />,
  document.getElementById("app")!,
);
