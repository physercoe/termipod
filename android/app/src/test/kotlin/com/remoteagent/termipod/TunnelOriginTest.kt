package com.remoteagent.termipod

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TunnelOriginTest {
    @Test fun acceptsOnlyTheForwardedHttpOrigin() {
        val origin = TunnelOrigin("http://127.0.0.1:32123/start")
        assertTrue(origin.valid)
        assertTrue(origin.allows("http://127.0.0.1:32123/form?x=1#end"))
        for (url in listOf("http://127.0.0.1:32124/", "http://localhost:32123/",
            "https://127.0.0.1:32123/", "http://example.com/", "file:///etc/passwd",
            "javascript:alert(1)", "intent://anything", "http://user@127.0.0.1:32123/")) {
            assertFalse(url, origin.allows(url))
        }
    }

    @Test fun rejectsInvalidInitialTargets() {
        for (url in listOf("", "http://example.com:1234/", "http://127.0.0.1/",
            "http://127.0.0.1:0/", "http://127.0.0.1:65536/", "https://127.0.0.1:1234/",
            "http://127.0.0.1:1234/with space", "http://user@127.0.0.1:1234/")) {
            assertFalse(url, TunnelOrigin(url).valid)
        }
    }
}
