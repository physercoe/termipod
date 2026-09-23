package com.remoteagent.termipod

import java.net.URI

/** Navigation policy shared by profile and compatibility browsers. */
internal class TunnelOrigin(url: String) {
    private val origin = runCatching { URI(url) }.getOrNull()
    val valid: Boolean = origin?.let {
        it.scheme == "http" && it.host == "127.0.0.1" && it.port in 1..65535 && it.userInfo == null
    } ?: false

    fun allows(url: String): Boolean = valid && runCatching {
        val target = URI(url)
        target.scheme == origin!!.scheme && target.host == origin.host &&
            target.port == origin.port && target.userInfo == null
    }.getOrDefault(false)
}
