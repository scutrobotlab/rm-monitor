package cn.scutbot.sim.rmdispatcher.data.lark

import com.fasterxml.jackson.annotation.JsonProperty

data class WebhookCard <T>(
    @JsonProperty("msg_type")
    val msgType: String = "interactive",

    val card : MessageCard<T>
)