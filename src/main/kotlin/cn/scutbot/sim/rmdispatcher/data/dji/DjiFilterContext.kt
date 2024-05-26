package cn.scutbot.sim.rmdispatcher.data.dji

data class DjiFilterContext(
    val match: Match,
    val meta: MutableMap<String, Any?> = mutableMapOf(),
    var hasNext: Boolean = true
) {
    inline fun <reified T> Get(key: String): T? {
        val value = meta[key]
        return if (value != null && value is T) {
            value
        } else {
            null
        }
    }

    inline fun <reified T> Set(key: String, value: T) {
        meta[key] = value
    }
}