package cn.scutbot.sim.rmdispatcher.listener.impl

import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.data.lark.CardVariable
import cn.scutbot.sim.rmdispatcher.data.lark.Score
import cn.scutbot.sim.rmdispatcher.listener.IMatchEndListener
import cn.scutbot.sim.rmdispatcher.service.IDjiService
import cn.scutbot.sim.rmdispatcher.service.ILarkService
import cn.scutbot.sim.rmdispatcher.service.IMatchService
import cn.scutbot.sim.rmdispatcher.utils.logger
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.scheduling.TaskScheduler
import org.springframework.scheduling.annotation.Async
import org.springframework.stereotype.Component
import java.time.Instant

@Component
@ConditionalOnProperty(prefix = "lark", name = ["enabled"], havingValue = "true")
class LarkMatchEndListener(
    @Autowired val larkService: ILarkService,
    @Autowired val djiService: IDjiService,
    @Autowired val taskScheduler: TaskScheduler,
    @Autowired val matchService: IMatchService,

    @Value("\${lark.cardId}") val cardId: String,
) : IMatchEndListener {

    @Async
    override fun onMatchEnd(match: Match) {
        logger().info("Match end: ${match.id ?: return}")

        taskScheduler.schedule({
            val scheduledMatch = djiService.fetchSchedule(match)
            val vars = matchService.getMatchCardVars(match.id) ?: CardVariable()
            val messageIds = matchService.getMessageIds(match.id)

            vars.scores = vars.scores ?: emptySet()
            vars.scores = vars.scores!! + Score(
                redScore = scheduledMatch?.redSideWinGameCount?.toString() ?: "0",
                blueScore = scheduledMatch?.blueSideWinGameCount?.toString() ?: "0",
            )
            vars.matchProgress = "结束"
            vars.color = "green"

            larkService.updateNotifyCard(messageIds.toSet(), cardId, vars)

        }, Instant.now().plusSeconds(2))
    }
}
