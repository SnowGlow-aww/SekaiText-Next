import { defineConfig } from 'vitepress'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// 兜底版本号：构建时从主仓库 package.json 读取（发版 bump 后重建自动更新）
const pkg = JSON.parse(
  readFileSync(fileURLToPath(new URL('../../package.json', import.meta.url)), 'utf-8'),
)

// GH Pages 主站用 '/'；OSS 镜像构建时 SITE_BASE=/site/
const base = process.env.SITE_BASE || '/'

export default defineConfig({
  lang: 'zh-CN',
  title: 'SekaiText Next',
  description:
    'Project Sekai 剧情翻译一站式工作台 — 翻译 · 校对 · 自动打轴 · 一键压制 · Live2D 剧情播放 · 术语库协作',
  base,
  head: [
    ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: `${base}favicon-32.png` }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '256x256', href: `${base}favicon.png` }],
    ['link', { rel: 'apple-touch-icon', sizes: '180x180', href: `${base}apple-touch-icon.png` }],
  ],
  vite: {
    define: {
      __APP_VERSION__: JSON.stringify(pkg.version),
    },
  },
  themeConfig: {
    logo: '/app-icon.png',
    siteTitle: 'SekaiText Next',
    // 链接统一带 .html：OSS 镜像是纯对象存储，无目录 rewrite（cleanUrls 会 404）
    nav: [
      { text: '首页', link: '/' },
      { text: '使用指南', link: '/guide/index.html' },
      { text: '插件', link: '/plugins.html' },
      { text: '下载', link: '/download.html' },
    ],
    sidebar: {
      '/guide/': [
        {
          text: '开始',
          items: [
            { text: '简介', link: '/guide/index.html' },
            { text: '安装与首次启动', link: '/guide/getting-started.html' },
          ],
        },
        {
          text: '核心功能',
          items: [
            { text: '剧情翻译', link: '/guide/editor.html' },
            { text: '自动打轴与压制', link: '/guide/autotiming.html' },
            { text: 'Live2D 剧情播放器', link: '/guide/live2d.html' },
            { text: '术语库与协作', link: '/guide/glossary.html' },
            { text: '插件市场', link: '/guide/plugins.html' },
          ],
        },
        {
          text: '其他',
          items: [{ text: '常见问题', link: '/guide/faq.html' }],
        },
      ],
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/SnowGlow-aww/SekaiText-Next' },
    ],
    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            noResultsText: '没有找到结果',
            resetButtonTitle: '清除查询',
            footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' },
          },
        },
      },
    },
    outline: { label: '本页目录', level: [2, 3] },
    docFooter: { prev: '上一页', next: '下一页' },
    lastUpdated: { text: '最后更新' },
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到亮色模式',
    darkModeSwitchTitle: '切换到暗色模式',
    sidebarMenuLabel: '菜单',
    returnToTopLabel: '回到顶部',
    footer: {
      message: '基于 MIT 协议发布 · 本项目仅用于学习和粉丝翻译目的',
      copyright: '「プロジェクトセカイ」是 SEGA / Colorful Palette 的商标',
    },
  },
  lastUpdated: true,
})
