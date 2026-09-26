import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'tests',workers:1,timeout:60000,use:{baseURL:process.env.UI_BASE_URL??'http://127.0.0.1:8080',headless:true,launchOptions:{args:['--enable-unsafe-swiftshader']},trace:'retain-on-failure'},reporter:'list'});
