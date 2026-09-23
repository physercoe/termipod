package com.remoteagent.termipod

import android.content.Context
import android.net.Uri
import android.view.View
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel
import io.flutter.plugin.common.StandardMessageCodec
import io.flutter.plugin.platform.PlatformView
import io.flutter.plugin.platform.PlatformViewFactory
import java.util.UUID
import java.io.ByteArrayInputStream

private const val viewType = "termipod/web_browser"
private const val profilePrefix = "termipod-tunnel-"

fun registerTunnelBrowser(engine: FlutterEngine) {
    val messenger = engine.dartExecutor.binaryMessenger
    // Remove only our abandoned profiles after process death, never the shared
    // canvas/default profile. Live profiles are created after registration.
    if (WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)) {
        val store = ProfileStore.getInstance()
        store.allProfileNames.filter { it.startsWith(profilePrefix) }.forEach {
            runCatching { store.deleteProfile(it) }
        }
    }
    MethodChannel(messenger, viewType).setMethodCallHandler { call, result ->
        if (call.method == "supported") {
            result.success(WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE))
        } else result.notImplemented()
    }
    engine.platformViewsController.registry.registerViewFactory(viewType,
        object : PlatformViewFactory(StandardMessageCodec.INSTANCE) {
            override fun create(context: Context, id: Int, args: Any?): PlatformView =
                TunnelBrowser(context, messenger, id, (args as? Map<*, *>)?.get("url") as? String ?: "")
        })
}

private class TunnelBrowser(context: Context, messenger: BinaryMessenger, id: Int,
                            private val initialUrl: String) : PlatformView {
    private val root = FrameLayout(context)
    private val channel = MethodChannel(messenger, "$viewType/$id")
    private val profile = profilePrefix + UUID.randomUUID().toString()
    private val origin = Uri.parse(initialUrl)
    private var web: WebView? = null

    init {
        if (WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE) &&
            origin.scheme == "http" && origin.host == "127.0.0.1" && origin.port in 1..65535) {
            val browser = WebView(context)
            // Must precede all other WebView operations. Cookies, caches,
            // service workers and local storage cannot cross tunnel profiles.
            WebViewCompat.setProfile(browser, profile)
            web = browser
            browser.settings.apply {
                javaScriptEnabled = true
                domStorageEnabled = true
                allowFileAccess = false
                allowContentAccess = false
                mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
                setSupportMultipleWindows(false)
                javaScriptCanOpenWindowsAutomatically = false
                setGeolocationEnabled(false)
            }
            // No JavaScript interface, file chooser, permission grants or TLS
            // bypass. Navigation is restricted; this is not a subresource proxy.
            browser.webViewClient = object : WebViewClient() {
                // Android does not call shouldOverrideUrlLoading for POST.
                // Intercept main-frame requests too, without blocking CDN assets.
                override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                    if (request.isForMainFrame && !allows(request.url)) {
                        root.post { channel.invokeMethod("error", "navigationBlocked") }
                        return WebResourceResponse("text/plain", "utf-8", 403, "Blocked",
                            emptyMap(), ByteArrayInputStream(ByteArray(0)))
                    }
                    return null
                }
                override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                    val url = request.url
                    val allowed = allows(url)
                    if (!allowed && request.isForMainFrame) channel.invokeMethod("error", "navigationBlocked")
                    return !allowed
                }
                override fun onPageFinished(view: WebView, url: String) { sendState() }
                override fun doUpdateVisitedHistory(view: WebView, url: String?, isReload: Boolean) { sendState() }
                override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                    if (request.isForMainFrame) channel.invokeMethod("error", "loadFailed")
                }
            }
            root.addView(browser, FrameLayout.LayoutParams(-1, -1))
        }
        channel.setMethodCallHandler { call, result ->
            val browser = web
            if (browser == null) result.error("unsupported", "Isolated browser unavailable", null)
            else {
                when (call.method) {
                    "load" -> browser.loadUrl(initialUrl)
                    // A user-triggered reload only; never replay automatically.
                    "reload" -> browser.reload()
                    "back" -> if (browser.canGoBack()) browser.goBack()
                    "forward" -> if (browser.canGoForward()) browser.goForward()
                    else -> { result.notImplemented(); return@setMethodCallHandler }
                }
                result.success(null)
            }
        }
    }

    private fun sendState() {
        channel.invokeMethod("state", mapOf("back" to (web?.canGoBack() ?: false),
            "forward" to (web?.canGoForward() ?: false)))
    }

    private fun allows(url: Uri): Boolean =
        url.scheme == origin.scheme && url.host == origin.host && url.port == origin.port

    override fun getView(): View = root

    override fun dispose() {
        channel.setMethodCallHandler(null)
        val browser = web
        web = null
        browser?.stopLoading()
        root.removeAllViews()
        browser?.destroy()
        if (browser != null) runCatching { ProfileStore.getInstance().deleteProfile(profile) }
    }
}
