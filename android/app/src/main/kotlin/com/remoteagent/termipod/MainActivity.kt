package com.remoteagent.termipod

import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.ResultReceiver
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private val channelName = "com.termipod.app/deeplink"
    private var methodChannel: MethodChannel? = null
    private var initialLink: String? = null
    private var browserResult: MethodChannel.Result? = null
    private var browserChannel: MethodChannel? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        registerTunnelBrowser(flutterEngine, this) { call, result ->
            if (browserResult != null) {
                result.error("browserBusy", "A compatibility browser is already open", null)
            } else if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) {
                result.error("unsupported", "Isolated browser requires Android 9 or newer", null)
            } else {
                val launch = Intent(this, CompatTunnelActivity::class.java)
                for (key in listOf("url", "close", "back", "forward", "reload", "loadFailed", "navigationBlocked")) {
                    launch.putExtra(key, call.argument<String>(key))
                }
                launch.putExtra("receiver", object : ResultReceiver(Handler(Looper.getMainLooper())) {
                    override fun onReceiveResult(code: Int, data: Bundle?) {
                        browserChannel?.invokeMethod("resume", null)
                    }
                })
                browserResult = result
                try {
                    @Suppress("DEPRECATION")
                    startActivityForResult(launch, 8142)
                } catch (error: Exception) {
                    browserResult = null
                    result.error("browserInitFailed", error.message, null)
                }
            }
        }
        browserChannel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "termipod/web_browser_compat")

        methodChannel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, channelName)
        methodChannel?.setMethodCallHandler { call, result ->
            if (call.method == "getInitialLink") {
                result.success(initialLink)
                initialLink = null
            } else {
                result.notImplemented()
            }
        }

        // Process intent on cold start
        initialLink = intent?.data?.toString()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // Hot link (app already running)
        val uri = intent.data?.toString()
        if (uri != null) {
            methodChannel?.invokeMethod("onDeepLink", uri)
        }
    }

    @Deprecated("Activity result bridge for the isolated native browser")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 8142) {
            val result = browserResult
            browserResult = null
            if (resultCode == RESULT_FIRST_USER) result?.error("browserInitFailed", "Browser initialization failed", null)
            else result?.success(null)
        }
    }
}
