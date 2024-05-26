package cn.scutbot.sim.rmdispatcher.config
import org.springframework.boot.context.properties.ConfigurationProperties

@ConfigurationProperties(prefix = "webhook")
data class WebhookConfig(
    var enabled: Boolean = false,
    var endpoints: Set<String> = emptySet()
)