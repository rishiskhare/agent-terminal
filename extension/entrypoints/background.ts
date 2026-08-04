import { JSONRPCResponse } from "~/entrypoints/shared/rpc"
import { type Browser } from 'wxt/browser';

export default defineBackground(() => {
  let _nativePort: Browser.runtime.Port | null = null;

  async function getNativePort(): Promise<Browser.runtime.Port | null> {
    if (_nativePort) {
      return _nativePort;
    }

    const port = browser.runtime.connectNative("com.github.pomdtr.tweety");

    const connected = await new Promise<boolean>((resolve) => {
      const onDisconnect = () => {
        port.onDisconnect.removeListener(onDisconnect);

        if (browser.runtime.lastError) {
          console.warn("Failed to connect:", browser.runtime.lastError.message);
          resolve(false);
        } else {
          console.warn("Disconnected from native messaging host");
          resolve(false);
        }
      };

      port.onDisconnect.addListener(onDisconnect);

      // Give the port a small window to disconnect if the host doesn't exist.
      setTimeout(() => {
        // Still connected after timeout — assume success
        resolve(true);
      }, 100); // 100ms is enough — disconnect happens almost instantly on failure
    });

    if (!connected) {
      return null;
    }

    _nativePort = port;

    _nativePort.onDisconnect.addListener(() => {
      _nativePort = null;
      if (browser.runtime.lastError) {
        console.warn("Port disconnected:", browser.runtime.lastError.message);
      } else {
        console.warn("Native messaging host disconnected");
      }
    });

    let { browserId } = await browser.storage.local.get<{ browserId?: string }>("browserId");
    if (!browserId) {
      browserId = generateSecureId(12);
      await browser.storage.local.set({ browserId });
    }

    await initialize(_nativePort, browserId);

    return _nativePort;
  }

  function initialize(port: Browser.runtime.Port, browserId: string) {
    return new Promise((resolve) => {
      const requestId = crypto.randomUUID();

      port.onMessage.addListener((message) => {
        if (!isJsonRpcResponse(message) || message.id !== requestId) {
          return;
        }

        return resolve(message);
      });

      port.postMessage({
        jsonrpc: "2.0",
        method: "initialize",
        id: requestId,
        params: {
          browserId,
          version: browser.runtime.getManifest().version,
        }
      })
    })
  }

  browser.runtime.onInstalled.addListener(async () => {
    browser.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true });
    await getNativePort();
  });

  // Ensure panel-on-click is set even if the extension was already installed.
  browser.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true });

  browser.runtime.onMessage.addListener((msg, sender, sendResponse) => {
    if (sender.id !== browser.runtime.id) {
      console.warn("Received message from unknown sender:", sender.id);
      return false; // Ignore messages from unknown senders
    }

    getNativePort().then((nativePort) => {
      if (!nativePort) {
        return sendResponse({
          jsonrpc: "2.0",
          id: msg.id || generateSecureId(12),
          error: {
            code: -32001,
            message: "Native host is not connected",
          }
        });
      }

      const listener = (res: unknown) => {
        if (typeof res !== "object" || res === null) {
          return;
        }

        if (!isJsonRpcResponse(res)) {
          return;
        }

        if (res.id !== msg.id) {
          return;
        }

        nativePort.onMessage.removeListener(listener);
        sendResponse(res);
      }

      nativePort.onMessage.addListener(listener)
      nativePort.postMessage(msg);
    })


    return true
  })

  const isJsonRpcResponse = (message: unknown): message is JSONRPCResponse => {
    if (typeof message !== "object" || message === null) {
      return false;
    }

    if (!("jsonrpc" in message) || message.jsonrpc !== "2.0") {
      return false;
    }

    if (!("id" in message) || typeof message.id !== "string") {
      return false;
    }

    if ("result" in message && (typeof message.result !== "object" || message.result === null)) {
      return false;
    }

    if ("error" in message && (typeof message.error !== "object" || message.error === null)) {
      return false;
    }

    return true;
  }

  function generateSecureId(length = 12) {
    const charset = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';
    const array = new Uint8Array(length);
    crypto.getRandomValues(array);
    return Array.from(array, (byte) => charset[byte % charset.length]).join('');
  }
});
