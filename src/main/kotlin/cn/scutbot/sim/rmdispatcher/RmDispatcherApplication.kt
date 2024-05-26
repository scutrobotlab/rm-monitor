package cn.scutbot.sim.rmdispatcher

import cn.scutbot.sim.rmdispatcher.config.WebhookConfig
import org.springframework.boot.autoconfigure.SpringBootApplication
import org.springframework.boot.context.properties.EnableConfigurationProperties
import org.springframework.boot.runApplication
import org.springframework.scheduling.annotation.EnableAsync
import org.springframework.scheduling.annotation.EnableScheduling

@SpringBootApplication
@EnableAsync
@EnableScheduling
@EnableConfigurationProperties(
    WebhookConfig::class
)
class RmDispatcherApplication

fun main(args: Array<String>) {
    runApplication<RmDispatcherApplication>(*args)
}
