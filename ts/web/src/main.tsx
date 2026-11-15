import { render } from "preact";

import { App } from "./app";
import "./style.css";

render(<App />, document.getElementById("app")!);

// a service worker needs HTTPS; over plain HTTP the page still works
if ("serviceWorker" in navigator && isSecureContext) {
  void navigator.serviceWorker.register("/sw.js");
}
