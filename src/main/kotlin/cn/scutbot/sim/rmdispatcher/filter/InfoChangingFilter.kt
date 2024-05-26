package cn.scutbot.sim.rmdispatcher.filter

import cn.scutbot.sim.rmdispatcher.data.dji.DjiFilterContext
import cn.scutbot.sim.rmdispatcher.data.dji.Match
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.core.annotation.Order
import org.springframework.data.redis.core.RedisTemplate
import org.springframework.data.redis.core.ValueOperations
import org.springframework.stereotype.Component
import java.util.concurrent.TimeUnit

@Component
@Order(0)
class InfoChangingFilter(
    @Autowired val redisTemplate: RedisTemplate<String, Match>
) : IDjiInfoFilter {
    val values: ValueOperations<String, Match> by lazy {
        redisTemplate.opsForValue()
    }

    override fun filter(context: DjiFilterContext): DjiFilterContext {
        val key = context.meta["key"] as String
        val previous = values.get(key) ?: Match.EMPTY
        if (!context.match.nameEquals(previous) || !context.match.scoreEquals(previous)) {
            context.meta["previous"] = previous
            context.meta["nameEquals"] = context.match.nameEquals(previous)
            context.meta["scoreEquals"] = context.match.scoreEquals(previous)
            context.meta["emptyPrevious"] = previous == Match.EMPTY
            context.meta["emptyCurrent"] = context.match == Match.EMPTY

            values.set(key, context.match, 1, TimeUnit.DAYS)
        } else {
            context.hasNext = false
        }

        return context
    }

    override fun condition(context: DjiFilterContext): Boolean = true
}
