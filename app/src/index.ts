import { spawn, ChildProcess } from 'child_process'
import { app, autoUpdater, dialog, Tray, Menu, BrowserWindow, MenuItemConstructorOptions } from 'electron'
import Store from 'electron-store'
import winston from 'winston'
import 'winston-daily-rotate-file'
import * as path from 'path'

import { analytics, id } from './telemetry'
import { installed } from './install'

require('@electron/remote/main').initialize()

if (require('electron-squirrel-startup')) {
  app.quit()
}

const store = new Store()

let welcomeWindow: BrowserWindow | null = null

declare const MAIN_WINDOW_WEBPACK_ENTRY: string

const logger = winston.createLogger({
  transports: [
    new winston.transports.Console(),
    new winston.transports.File({
      filename: path.join(app.getPath('home'), '.ollama', 'logs', 'server.log'),
      maxsize: 1024 * 1024 * 20,
      maxFiles: 5,
    }),
  ],
  format: winston.format.printf(info => info.message),
})

app.on('ready', () => {
  const gotTheLock = app.requestSingleInstanceLock()
  if (!gotTheLock) {
    app.exit(0)
    return
  }

  app.on('second-instance', () => {
    if (app.hasSingleInstanceLock()) {
      app.releaseSingleInstanceLock()
    }

    if (proc) {
      proc.off('exit', restart)
      proc.kill()
    }

    app.exit(0)
  })

  app.focus({ steal: true })

  init()
})

function firstRunWindow() {
  // Create the browser window.
  welcomeWindow = new BrowserWindow({
    width: 400,
    height: 500,
    frame: false,
    fullscreenable: false,
    resizable: false,
    movable: true,
    show: false,
    webPreferences: {
      nodeIntegration: true,
      contextIsolation: false,
    },
    alwaysOnTop: true,
  })

  require('@electron/remote/main').enable(welcomeWindow.webContents)

  welcomeWindow.loadURL(MAIN_WINDOW_WEBPACK_ENTRY)
  welcomeWindow.on('ready-to-show', () => welcomeWindow.show())
}

let tray: Tray | null = null

function setTray(updateAvailable: boolean) {
  const updateItems: MenuItemConstructorOptions[] = [
    { label: 'An update is available', enabled: false },
    {
      label: 'Restart to update',
      click: () => autoUpdater.quitAndInstall(),
    },
    { type: 'separator' },
  ]

  const menu = Menu.buildFromTemplate([
    ...(updateAvailable ? updateItems : []),
    { role: 'quit', label: 'Quit Ollama', accelerator: 'Command+Q' },
  ])

  const iconPath = app.isPackaged
    ? updateAvailable
      ? path.join(process.resourcesPath, 'iconUpdateTemplate.png')
      : path.join(process.resourcesPath, 'iconTemplate.png')
    : updateAvailable
    ? path.join(__dirname, '..', '..', 'assets', 'iconUpdateTemplate.png')
    : path.join(__dirname, '..', '..', 'assets', 'iconTemplate.png')

  if (!tray) {
    tray = new Tray(iconPath)
  }

  tray.setToolTip(updateAvailable ? 'An update is available' : 'Ollama')
  tray.setContextMenu(menu)
  tray.setImage(iconPath)
}

let proc: ChildProcess = null

function server() {
  const binary = app.isPackaged
    ? path.join(process.resourcesPath, 'ollama')
    : path.resolve(process.cwd(), '..', 'ollama')

  proc = spawn(binary, ['serve'])

  proc.stdout.on('data', data => {
    logger.info(data.toString().trim())
  })

  proc.stderr.on('data', data => {
    logger.error(data.toString().trim())
  })

  proc.on('exit', restart)
}

function restart() {
  setTimeout(server, 1000)
}

app.on('before-quit', () => {
  if (proc) {
    proc.off('exit', restart)
    proc.kill()
  }
})

function init() {
  if (app.isPackaged) {
    heartbeat()
    autoUpdater.checkForUpdates()
    setInterval(() => {
      heartbeat()
      autoUpdater.checkForUpdates()
    }, 60 * 60 * 1000)
  }

  setTray(false)

  if (process.platform === 'darwin') {
    if (app.isPackaged) {
      if (!app.isInApplicationsFolder()) {
        const chosen = dialog.showMessageBoxSync({
          type: 'question',
          buttons: ['Move to Applications', 'Do Not Move'],
          message: 'Ollama works best when run from the Applications directory.',
          defaultId: 0,
          cancelId: 1,
        })

        if (chosen === 0) {
          try {
            app.moveToApplicationsFolder({
              conflictHandler: conflictType => {
                if (conflictType === 'existsAndRunning') {
                  dialog.showMessageBoxSync({
                    type: 'info',
                    message: 'Cannot move to Applications directory',
                    detail:
                      'Another version of Ollama is currently running from your Applications directory. Close it first and try again.',
                  })
                }
                return true
              },
            })
            return
          } catch (e) {
            logger.error(`[Move to Applications] Failed to move to applications folder - ${e.message}}`)
          }
        }
      }
    }
  }

  server()

  if (store.get('first-time-run') && installed()) {
    if (process.platform === 'darwin') {
      app.dock.hide()
    }

    app.setLoginItemSettings({ openAtLogin: app.getLoginItemSettings().openAtLogin })
    return
  }

  // This is the first run or the CLI is no longer installed
  app.setLoginItemSettings({ openAtLogin: true })
  firstRunWindow()
}

// Quit when all windows are closed, except on macOS. There, it's common
// for applications and their menu bar to stay active until the user quits
// explicitly with Cmd + Q.
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') {
    app.quit()
  }
})

// In this file you can include the rest of your app's specific main process
// code. You can also put them in separate files and import them here.
autoUpdater.setFeedURL({
  url: `https://ollama.ai/api/update?os=${process.platform}&arch=${process.arch}&version=${app.getVersion()}`,
})

async function heartbeat() {
  analytics.track({
    anonymousId: id(),
    event: 'heartbeat',
    properties: {
      version: app.getVersion(),
    },
  })
}

autoUpdater.on('error', e => {
  console.error(`update check failed - ${e.message}`)
})

autoUpdater.on('update-downloaded', () => {
  setTray(true)
})
