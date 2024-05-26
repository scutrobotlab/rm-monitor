package cn.scutbot.sim.rmdispatcher

import cn.scutbot.sim.rmdispatcher.data.dji.*
import cn.scutbot.sim.rmdispatcher.data.lark.CardVariable
import cn.scutbot.sim.rmdispatcher.data.lark.Score
import cn.scutbot.sim.rmdispatcher.listener.impl.WebhookMatchStartListener
import cn.scutbot.sim.rmdispatcher.service.ILarkService
import cn.scutbot.sim.rmdispatcher.service.IWebhookService
import cn.scutbot.sim.rmdispatcher.utils.logger
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.data.redis.core.RedisTemplate

@SpringBootTest
class RmDispatcherApplicationTests {
    val match = Match(
        id = "18995",
        round = 2,
        totalRound = 3,
        orderNumber = 20,
        matchType = "GROUP",
        blueSideId = "38115",
        blueSide = Side(
            id = "38115",
            fillStatus = "DONE",
            fillSourceId = "2181",
            fillSourceType = "Group",
            fillSourceNumber = "8",
            playerId = "9216",
            player = Player(
                name = "A8",
                team = Team(
                    id = "183",
                    collegeLogo = "https://rm-static.djicdn.com/games-backend/2405986e-23a2-4c27-b8e5-04faf04bf2fa",
                    collegeName = "南京航空航天大学金城学院",
                    name = "Born of Fire"
                )
            )
        ),
        redSideId = "38114",
        redSide = Side(
            id = "38114",
            fillStatus = "DONE",
            fillSourceId = "2181",
            fillSourceType = "Group",
            fillSourceNumber = "7",
            playerId = "9209",
            player = Player(
                name = "A4",
                team = Team(
                    id = "755",
                    collegeLogo = "https://rm-static.djicdn.com/games-backend/01c74eed-fc6a-425d-80ef-15b9ef1ec1d4",
                    collegeName = "齐鲁工业大学",
                    name = "Adam"
                )
            )
        ),
        redSideScore = 0,
        blueSideScore = 0,
        blueSideWinGameCount = 1,
        planGameCount = 3,
        planStartedAt = "2024-05-16T00:30:00Z",
        redSideWinGameCount = 0,
        winnerPlaceholdName = null,
        loserPlaceholdName = null,
        slug = null,
        slugName = "2",
        status = "WAITING",
        zone = Zone(
            id = "498",
            name = "东部赛区",
            zoneType = "GROUP_ZONE",
            eventId = "174",
            event = ZoneEvent(
                id = "174",
                title = "RoboMaster 2024超级对抗赛"
            )
        )
    )

    @Test
    fun larkTest(
        @Autowired larkService: ILarkService
    ) {
        val vars = CardVariable(match)
        larkService.uploadImg(match.redSide?.player?.team?.collegeLogo)?.let {
            vars.redAvatar = it
        }

        larkService.uploadImg(match.blueSide?.player?.team?.collegeLogo)?.let {
            vars.blueAvatar = it
        }

        val messageIds = larkService.notifyCard("AAqkpd7LuaV0s", vars, emptySet(), larkService.webhooks())

        logger().info(messageIds.toString())
    }

    @Test
    fun redisTest(
        @Autowired redisTemplate: RedisTemplate<String, CardVariable>,
    ) {
        val vars = CardVariable(
            "awa"
        )

        vars.scores = setOf(
            Score("1", "2")
        )

        logger().info(vars.toString())

        redisTemplate.opsForValue().set("test", vars)

        val result = redisTemplate.opsForValue().get("test")

        logger().info(result.toString())
    }

    @Test
    fun webhookTest(
        @Autowired webhookService: IWebhookService
    ) {
        webhookService.sendWebhooks(WebhookMatchStartListener.WEBHOOK_TYPE, match)
    }
}
