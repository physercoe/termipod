import Flutter
import UIKit
import WebKit

/// A separate in-memory data store per browser; no cookies or local storage
/// are shared with another tunnel or the canvas artifact WebView.
final class TunnelBrowserFactory: NSObject, FlutterPlatformViewFactory {
  private let messenger: FlutterBinaryMessenger
  init(messenger: FlutterBinaryMessenger) { self.messenger = messenger }

  static func register(with registrar: FlutterPluginRegistrar) {
    let messenger = registrar.messenger()
    FlutterMethodChannel(name: "termipod/web_browser", binaryMessenger: messenger)
      .setMethodCallHandler { call, result in
        if call.method == "supported" { result(true) }
        else { result(FlutterMethodNotImplemented) }
      }
    registrar.register(TunnelBrowserFactory(messenger: messenger), withId: "termipod/web_browser")
  }

  func createArgsCodec() -> FlutterMessageCodec & NSObjectProtocol { FlutterStandardMessageCodec.sharedInstance() }
  func create(withFrame frame: CGRect, viewIdentifier viewId: Int64, arguments args: Any?) -> FlutterPlatformView {
    let url = (args as? [String: Any])?["url"] as? String ?? ""
    return TunnelBrowser(frame: frame, id: viewId, messenger: messenger, url: url)
  }
}

final class TunnelBrowser: NSObject, FlutterPlatformView, WKNavigationDelegate {
  private let web: WKWebView
  private let channel: FlutterMethodChannel
  private let initialURL: URL?

  init(frame: CGRect, id: Int64, messenger: FlutterBinaryMessenger, url: String) {
    let config = WKWebViewConfiguration()
    config.websiteDataStore = .nonPersistent()
    config.preferences.javaScriptCanOpenWindowsAutomatically = false
    web = WKWebView(frame: frame, configuration: config)
    initialURL = URL(string: url)
    channel = FlutterMethodChannel(name: "termipod/web_browser/\(id)", binaryMessenger: messenger)
    super.init()
    web.navigationDelegate = self
    // No script message handler, file access, native permission grant or
    // authentication-challenge override is exposed to remote content.
    channel.setMethodCallHandler { [weak self] call, result in
      guard let self = self else { result(nil); return }
      switch call.method {
      case "load":
        if let url = self.initialURL, url.scheme == "http", url.host == "127.0.0.1",
           let port = url.port, (1...65535).contains(port) {
          self.web.load(URLRequest(url: url))
        }
      case "reload": self.web.reload()
      case "back": self.web.goBack()
      case "forward": self.web.goForward()
      default: result(FlutterMethodNotImplemented); return
      }
      result(nil)
    }
  }

  func view() -> UIView { web }

  func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
               decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
    let url = navigationAction.request.url
    let allowed = url?.scheme == initialURL?.scheme && url?.host == initialURL?.host && url?.port == initialURL?.port
    if !allowed { channel.invokeMethod("error", arguments: "navigationBlocked") }
    decisionHandler(allowed ? .allow : .cancel)
  }

  func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
    channel.invokeMethod("state", arguments: ["back": web.canGoBack, "forward": web.canGoForward])
  }
  func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
    channel.invokeMethod("error", arguments: "loadFailed")
  }
  func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
    if (error as NSError).code != NSURLErrorCancelled { channel.invokeMethod("error", arguments: "loadFailed") }
  }

  deinit {
    channel.setMethodCallHandler(nil)
    web.stopLoading()
    web.navigationDelegate = nil
  }
}
