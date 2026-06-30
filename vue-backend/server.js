import express from 'express'
import cors from 'cors'

const app = express()
const port = 8079 // 改成 8079，和 8080 紧挨着

app.use(cors())
app.use(express.json())

// ========== API 路由 ==========

app.get('/api/status', (req, res) => {
  res.json({
    running: true,
    memory: 2.1,
    uptime: '2h 30m 15s',
    players: 3,
    maxPlayers: 20
  })
})

app.post('/api/server/start', (req, res) => {
  res.json({
    success: true,
    message: '服务器启动成功'
  })
})

app.post('/api/server/stop', (req, res) => {
  res.json({
    success: true,
    message: '服务器已停止'
  })
})

app.get('/api/logs', (req, res) => {
  res.json({
    logs: [
      '[14:32:01] 服务器启动',
      '[14:32:05] 玩家 Steve 加入游戏',
      '[14:35:12] 玩家 Alex 加入游戏',
      '[14:40:33] 服务器保存中...',
      '[14:45:20] 玩家 Herobrine 加入了游戏 😱'
    ]
  })
})

app.listen(port, () => {
  console.log(`🔵 Vue 后端服务启动于 http://127.0.0.1:${port}`)
})