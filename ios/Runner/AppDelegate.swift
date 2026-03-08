import Flutter
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate {
  private var deepLinkChannel: FlutterMethodChannel?
  private var initialLink: String?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    GeneratedPluginRegistrant.register(with: self)

    let controller = window?.rootViewController as! FlutterViewController
    deepLinkChannel = FlutterMethodChannel(
      name: "com.muxpod.app/deeplink",
      binaryMessenger: controller.binaryMessenger
    )

    deepLinkChannel?.setMethodCallHandler { [weak self] call, result in
      if call.method == "getInitialLink" {
        result(self?.initialLink)
        self?.initialLink = nil
      } else {
        result(FlutterMethodNotImplemented)
      }
    }

    // Check cold-start URL
    if let url = launchOptions?[.url] as? URL {
      initialLink = url.absoluteString
    }

    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  override func application(
    _ app: UIApplication,
    open url: URL,
    options: [UIApplication.OpenURLOptionsKey: Any] = [:]
  ) -> Bool {
    // Hot link (app already running)
    deepLinkChannel?.invokeMethod("onDeepLink", arguments: url.absoluteString)
    return super.application(app, open: url, options: options)
  }
}
