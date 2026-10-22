import * as routes from "./routes.js";
import { parse } from "error-stack-parser-es";
import { Mutex } from "async-mutex";
import browserType from "consts:browserType";

async function tryMatchRoute(req) {
  for (let route of routes.sortedRoutes) {
    const vars = route.__match(req.path);

    if (!vars) {
      continue;
    }

    const routeFunc = route[req.method.toLowerCase()];

    if (!routeFunc) {
      return { status: 404 };
    }

    return await routeFunc({ ...req, ...vars });
  }

  return { status: 404 };
}

let port;
const now = new Date();
const mutex = new Mutex();

async function onMessage(req, messageSender) {
  req.message_sender = messageSender;
  await mutex.runExclusive(async () => onMessageHTTP(req));
}

const DEFAULT_TIMEOUT = 1000;

function getRouteTimeout(path) {
  for (let route of routes.sortedRoutes) {
    if (route.__match(path)) {
      return route.__timeout || DEFAULT_TIMEOUT;
    }
  }
  return DEFAULT_TIMEOUT;
}

async function onMessageHTTP(req) {
  let results = await browser.storage.sync.get("browser_id");

  if (results === undefined || results["browser_id"] === undefined) {
    // TODO ERROR
  } else {
    req.browser_id = {
      browser: browserType,
      id: results["browser_id"],
    };
  }

  let routeTimeout = getRouteTimeout(req.path);
  let response = await Promise.race([timeout(routeTimeout), runRoute(req)]);

  response.headers = {
    "X-Chrest-Startup-Time": now.toISOString(),
    "X-Chrest-Browser-Type": browserType,
  };

  response.type = "http";

  port.postMessage(response);
}

async function runRoute(req) {
  return tryMatchRoute(req).catch((e) => {
    console.error(e);
    return {
      status: 500,
      body: {
        error: {
          message: e.message,
          stack: parse(e),
        },
      },
    };
  });
}

async function timeout(delay) {
  let timeoutResponse = {
    status: 500,
    body: {
      error: {
        message: "timeout",
      },
    },
  };

  return new Promise((resolve) => {
    setTimeout(resolve, delay, timeoutResponse);
  });
}

if (typeof browser == "undefined") {
  // Chrome does not support the browser namespace yet.
  globalThis.browser = chrome;
}

function browserIdFromSettingString(v) {
  return `${browserType}-${v}`;
}

async function notifyMe(title, message) {
  let opt = {
    type: "basic",
    title: title,
    message: message,
    iconUrl: "chrest_icon_128.png",
  };

  await browser.notifications.create(null, opt);
}

async function initialize(e) {
  browser.storage.sync.onChanged.addListener((changes) => {
    console.log(changes);
    let browser_id = changes["browser_id"];

    if (browser_id == undefined) {
      return;
    }

    initializePort(browser_id.newValue);
  });

  let results = await browser.storage.sync.get("browser_id");

  if (results === undefined || results["browser_id"] === undefined) {
    browser.runtime.openOptionsPage();
  } else {
    await initializePort(results["browser_id"]);
  }
}

async function deinitializePort() {
  if (port != undefined) {
    console.log("disconnecting from native host");
    port.disconnect();
  }
}

async function initializePort(browser_id) {
  await deinitializePort();

  console.log(`try connect: ${JSON.stringify(browser_id)}`);
  port = browser.runtime.connectNative("com.linenisgreat.code.chrest");
  port.onMessage.addListener(onMessage);

  port.onDisconnect.addListener(async (disconnected) => {
    // Firefox reports the reason on the port; Chrome on runtime.lastError.
    let error = disconnected?.error ?? browser.runtime.lastError;
    let msg = error ? error.message : "unknown reason";
    console.error(`native host disconnected: ${msg}`);
    port = undefined;
    await notifyMe("Chrest", `Native host disconnected: ${msg}`);
  });

  let browserId = browserIdFromSettingString(browser_id);
  console.log(browserId);

  port.postMessage({
    type: "who-am-i",
    browser_id: browserId,
  });

  console.log("native host connected");
  await notifyMe("Chrest", "Native host connected");
}

browser.runtime.onStartup.addListener(() => {
  console.log("on startup");
});

browser.runtime.onInstalled.addListener(() => {
  console.log("on installed");
});

console.log("main");

initialize({ reason: "startup" });
