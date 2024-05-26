package cn.scutbot.sim.rmdispatcher.controllers

import cn.scutbot.sim.rmdispatcher.service.IMatchService
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.RestController

@RestController("/match")
@ConditionalOnProperty(prefix = "lark", name = ["enabled"], havingValue = "true")
class MatchController(
    @Autowired val matchService: IMatchService
) {
    @GetMapping("/messageId")
    fun getMessageIds(@RequestParam matchId: String) =
        matchService.getMessageIds(matchId)
}