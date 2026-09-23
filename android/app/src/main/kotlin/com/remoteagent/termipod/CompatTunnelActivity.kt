package com.remoteagent.termipod

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Process
import android.os.ResultReceiver
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import java.io.ByteArrayInputStream
import java.util.UUID

/** A single browsing session in a dedicated process for pre-profile WebViews.
 * Never initialize WebView in this process before choosing its data directory.
 * The Flutter process continues to own SSH and loopback listeners.
 */
class CompatTunnelActivity : Activity() {
    companion object {
        private const val prefix = "termipod-compat-"
        private val suffix = prefix + UUID.randomUUID().toString()
        private var initialized = false
        private var activeUrl: String? = null
    }

    private var web: WebView? = null
    private var receiver: ResultReceiver? = null
    private lateinit var status: TextView
    private lateinit var origin: TunnelOrigin
    private var loadError = ""
    private var blockedError = ""

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) { finish(); return }
        val url = intent.getStringExtra("url") ?: ""
        origin = TunnelOrigin(url)
        if (!origin.valid ||
            (activeUrl != null && activeUrl != url)) {
            finish(); return
        }
        @Suppress("DEPRECATION")
        receiver = intent.getParcelableExtra("receiver")
        try {
            if (!initialized) {
                // Only our abandoned compatibility directories, before any
                // WebView exists. Never touch canvas/default/named profiles.
                applicationInfo.dataDir.let { path ->
                    val root = java.io.File(path)
                    root.listFiles()?.filter {
                        it.name.startsWith("app_webview_$prefix") &&
                            it.canonicalFile.parentFile == root.canonicalFile
                    }?.forEach { it.deleteRecursively() }
                }
                WebView.setDataDirectorySuffix(suffix)
                initialized = true
                activeUrl = url
            }
            buildBrowser(url)
        } catch (_: Exception) {
            setResult(RESULT_FIRST_USER, Intent().putExtra("error", "browserInitFailed"))
            finish()
        }
    }

    private fun buildBrowser(url: String) {
        val root = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val toolbar = LinearLayout(this)
        fun button(label: String, action: () -> Unit) {
            toolbar.addView(Button(this).apply {
                text = label
                contentDescription = label
                setOnClickListener { action() }
            }, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
        }
        button(intent.getStringExtra("close") ?: "Close") { closeBrowser() }
        button(intent.getStringExtra("back") ?: "Back") { web?.let { if (it.canGoBack()) it.goBack() } }
        button(intent.getStringExtra("forward") ?: "Forward") { web?.let { if (it.canGoForward()) it.goForward() } }
        button(intent.getStringExtra("reload") ?: "Reload") {
            // A reload is always a user action, never an automatic POST replay.
            status.text = ""
            web?.reload()
        }
        root.addView(toolbar)
        status = TextView(this)
        root.addView(status)
        loadError = intent.getStringExtra("loadFailed") ?: "Page could not load"
        blockedError = intent.getStringExtra("navigationBlocked") ?: "Navigation blocked"
        val browser = WebView(this)
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
        browser.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val blocked = !allows(request.url)
                if (blocked && request.isForMainFrame) status.text = blockedError
                return blocked
            }
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                if (request.isForMainFrame && !allows(request.url)) {
                    runOnUiThread { status.text = blockedError }
                    return WebResourceResponse("text/plain", "utf-8", 403, "Blocked",
                        emptyMap(), ByteArrayInputStream(ByteArray(0)))
                }
                return null
            }
            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame) status.text = loadError
            }
            override fun onRenderProcessGone(view: WebView, detail: android.webkit.RenderProcessGoneDetail): Boolean {
                setResult(RESULT_FIRST_USER, Intent().putExtra("error", "browserInitFailed"))
                finish()
                return true
            }
        }
        root.addView(browser, LinearLayout.LayoutParams(-1, 0, 1f))
        setContentView(root)
        browser.loadUrl(url)
    }

    private fun allows(url: Uri): Boolean =
        origin.allows(url.toString())

    override fun onResume() {
        super.onResume()
        // Restoring this native screen does not resume Flutter's Activity.
        // Notify Dart to probe/recover SSH without automatically reloading.
        receiver?.send(1, Bundle())
    }

    @Suppress("DEPRECATION")
    override fun onBackPressed() = closeBrowser()

    private fun closeBrowser() {
        setResult(RESULT_OK)
        finish()
    }

    override fun onDestroy() {
        web?.stopLoading()
        web?.destroy()
        web = null
        super.onDestroy()
        // A WebView data suffix cannot be switched within a living process.
        // End only this dedicated browser process, never Flutter/SSH.
        if (isFinishing) Process.killProcess(Process.myPid())
    }
}
