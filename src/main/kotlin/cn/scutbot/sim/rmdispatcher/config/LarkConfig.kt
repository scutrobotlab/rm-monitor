package cn.scutbot.sim.rmdispatcher.config

import com.lark.oapi.Client
import com.lark.oapi.core.cache.ICache
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.data.redis.core.StringRedisTemplate
import java.util.concurrent.TimeUnit

@Configuration
@ConfigurationProperties(prefix = "lark")
data class LarkConfig(
    var enabled: Boolean = false,
    var appId: String = "",
    var appSecret: String = "",
    var cardId: String = "",
    var webhooks: Set<String> = emptySet()
) {
    @Bean
    fun larkClient(
        @Autowired larkConfig: LarkConfig,
        stringRedisTemplate: StringRedisTemplate,
    ): Client =
        Client.newBuilder(appId, appSecret).tokenCache(
            object : ICache {
                override fun get(key: String): String? =
                    stringRedisTemplate.opsForValue()[key]

                override fun set(key: String, value: String, expire: Int, timeUnit: TimeUnit) =
                    stringRedisTemplate.opsForValue().set(key, value, expire.toLong(), timeUnit)
            }
        ).build()
}
