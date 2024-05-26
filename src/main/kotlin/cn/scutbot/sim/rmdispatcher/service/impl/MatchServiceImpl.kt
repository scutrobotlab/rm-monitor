package cn.scutbot.sim.rmdispatcher.service.impl

import cn.scutbot.sim.rmdispatcher.data.lark.CardVariable
import cn.scutbot.sim.rmdispatcher.service.IMatchService
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.data.redis.core.RedisTemplate
import org.springframework.data.redis.core.StringRedisTemplate
import org.springframework.stereotype.Service

@Service
class MatchServiceImpl(
    @Autowired val redisTemplate: RedisTemplate<String, CardVariable>,
    @Autowired val stringRedisTemplate: StringRedisTemplate,
) : IMatchService {
    override fun getMessageIds(matchId: String): Set<String> {
        val messageIds = stringRedisTemplate.opsForList().range("match:messages:$matchId", 0, -1) ?: emptyList()

        return messageIds.toSet()
    }

    override fun getMatchCardVars(matchId: String): CardVariable? {
        return redisTemplate.opsForValue().get("match:vars:$matchId")
    }

    override fun saveMessageIds(matchId: String, messageIds: Set<String>) {
        stringRedisTemplate.opsForList().rightPushAll("match:messages:$matchId", messageIds)
    }

    override fun saveMatchCardVars(matchId: String, vars: CardVariable) {
        redisTemplate.opsForValue().set("match:vars:$matchId", vars)
    }
}