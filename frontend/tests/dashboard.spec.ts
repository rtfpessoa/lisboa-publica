import {test,expect} from '@playwright/test';
import path from 'node:path';
// These cases intentionally use real collected data and an optional development account.
const liveOnly=()=>test.skip(!process.env.UI_LIVE_DATA,'Set UI_LIVE_DATA=1 with a running populated backend for live acceptance.');
const shot=(name:string)=>path.resolve('..','docs','acceptance',name+'.png');
test('desktop public dashboard, independent sources, search, tabs, charts and fleet',async({page})=>{
 liveOnly();test.setTimeout(120000); // Full public-source journey includes several bounded remote queries.
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
 await page.setViewportSize({width:1440,height:900});await page.goto('/');
 await expect(page.getByText('Veículos reportados',{exact:true})).toBeVisible();
 await expect(page.locator('.map canvas')).toBeVisible();
 await expect.poll(async()=>page.locator('.map').evaluate(e=>e.getBoundingClientRect().height)).toBe(900);
 await page.waitForResponse(r=>r.url().includes('tiles.openfreemap.org/planet/')&&r.status()===200,{timeout:30000}).catch(()=>{});
 await page.waitForTimeout(3000);await page.screenshot({path:shot('desktop')});
 await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true');
 await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.locator('.main-operator').first().click();
 await page.getByRole('button',{name:'Expandir métricas'}).click();await expect(page.getByText('Frequência operacional',{exact:true})).toBeVisible();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Alameda');await expect(page.locator('.search-results button').first()).toBeVisible();
 await page.locator('.search-results button').first().click();await expect(page.locator('.detail-panel')).toBeVisible();await page.getByRole('button',{name:'Fechar detalhes'}).click();
 await page.getByLabel('Pesquisar carreira ou paragem').fill('728');await expect(page.locator('.search-results button').first()).toBeVisible();await page.locator('.search-results button').first().click();await expect(page.locator('.detail-panel')).toBeVisible();await page.getByRole('button',{name:'Viagens planeadas',exact:true}).click();await expect(page.locator('.detail-panel tbody tr').first()).toBeVisible();await expect(page.locator('.detail-panel tbody tr')).toHaveCount(25);await page.locator('.detail-panel').getByRole('button',{name:'Seguinte',exact:true}).click();await expect(page.locator('.detail-panel tbody tr').first()).toBeVisible();await page.getByRole('button',{name:'Fechar detalhes'}).click();
 await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(page.getByRole('heading',{name:'Histórico',exact:true})).toBeVisible();await expect(page.locator('.recharts-surface').first()).toBeVisible();await page.getByLabel('Dia do histórico').fill('');await expect(page.getByLabel('Dia do histórico')).not.toHaveValue('');await page.waitForTimeout(1800);await page.screenshot({path:shot('history')});
 await expect(page.getByRole('button',{name:'% Viagens',exact:true})).toBeDisabled();await expect(page.locator('#history-capability')).toBeVisible();
 await page.getByRole('button',{name:'Trânsito',exact:true}).click();await expect(page.getByRole('heading',{name:'Trânsito',exact:true})).toBeVisible();await page.getByLabel('Excluir fins de semana').check();await page.getByLabel('Mostrar terminais').check();await expect(page.getByText(/Identificação de terminais indisponível/)).toBeVisible();await page.screenshot({path:shot('traffic')});
 await page.getByRole('button',{name:'Remover filtro de carreira'}).click();await page.getByRole('button',{name:'Frota',exact:true}).click();await expect(page.getByRole('heading',{name:'Análise de Frota'})).toBeVisible();await page.getByLabel('Dia da frota').fill('');await expect(page.getByLabel('Dia da frota')).not.toHaveValue('');
 await page.getByRole('button',{name:'Veículos',exact:true}).click();await expect(page.locator('.page-panel tbody tr').first()).toBeVisible();await page.getByLabel('Ordenar frota').selectOption('distance');
 const next=page.getByRole('button',{name:'Seguinte',exact:true});await expect(next).toBeEnabled({timeout:20000});await next.click();await expect(page.getByText(/página 2/)).toBeVisible();await page.getByLabel('Filtrar frota').fill('not-a-real-vehicle');await expect(page.getByText('Sem veículos detetados neste intervalo.')).toBeVisible();await page.getByLabel('Filtrar frota').fill('');await page.getByRole('button',{name:'Modelos',exact:true}).click();await expect(page.getByRole('heading',{name:'Distribuição por modelos publicados'})).toBeVisible();await page.getByRole('button',{name:'Estações de recolha',exact:true}).click();await expect(page.locator('.page-panel').getByText(/Estas estações de recolha são diferentes/)).toBeVisible();await page.getByRole('button',{name:'Tipologias',exact:true}).click();await expect(page.getByRole('heading',{name:'Tipologias publicadas'})).toBeVisible();await page.getByRole('button',{name:'Visão Geral',exact:true}).click();await page.screenshot({path:shot('fleet')});
 await page.getByRole('button',{name:'Fontes e disponibilidade',exact:true}).click();await expect(page.getByRole('dialog',{name:'Fontes e disponibilidade'})).toBeVisible();await expect(page.getByText('API direta do Metro',{exact:true})).toBeVisible();await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(errors).toEqual([]);
});
test('optional local account can create and revoke scoped keys',async({page})=>{
 liveOnly();const keyName='Browser validation '+Date.now();await page.goto('/');const config=await (await page.request.get('/api/v1/config')).json();await page.getByRole('button',{name:'Conta e chaves API',exact:true}).click();
 const local=page.getByRole('button',{name:'Sessão de desenvolvimento local'});test.skip(!config.dev_auth,'Local development login deliberately disabled');await expect(local).toBeVisible();
 await local.click();await expect(page.getByRole('button',{name:'Sair',exact:true})).toBeVisible();await page.getByLabel('Nome da chave').fill(keyName);await page.getByRole('button',{name:'Criar chave (30 dias)'}).click();await expect(page.locator('.secret code')).toContainText('lp_');await page.getByRole('button',{name:'Ocultar',exact:true}).click();await page.locator('.key-list article').filter({hasText:keyName}).last().getByRole('button',{name:'Revogar',exact:true}).click();await expect(page.locator('.key-list article').filter({hasText:keyName}).last().getByRole('button',{name:'Revogada'})).toBeDisabled();await page.getByRole('button',{name:'Sair',exact:true}).click();await expect(page.getByRole('button',{name:'Sessão de desenvolvimento local'})).toBeVisible();
});
test('mobile navigation, panels and map fit the viewport',async({page})=>{
 await page.setViewportSize({width:390,height:844});await page.goto('/');await expect(page.getByRole('button',{name:'Abrir operadores'})).toBeVisible();await expect(page.locator('.map canvas')).toBeVisible();await page.waitForTimeout(2500);await page.screenshot({path:shot('mobile')});
 for(const view of ['Histórico','Trânsito','Frota','Tempo real']){await page.locator('.compact-nav').getByRole('button',{name:view,exact:true}).click();await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true)}
 await page.getByRole('button',{name:'Abrir pesquisa'}).click();await expect(page.getByLabel('Pesquisar carreira ou paragem')).toBeVisible();await page.getByRole('button',{name:'Conta e chaves API',exact:true}).click();await expect(page.getByRole('dialog',{name:'Conta e chaves API'})).toBeVisible();await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0);
});
test('Metro station uses one shared SSE interest without board polling',async({page})=>{
 liveOnly();const requests:string[]=[];page.on('request',r=>requests.push(new URL(r.url()).pathname));
 await page.goto('/');await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await expect(page.locator('.search-results button').first()).toBeVisible();await page.locator('.search-results button').first().click();
 await expect(page.locator('.station-popup')).toHaveAttribute('data-metro-revision',/./);
 const streams=requests.filter(p=>p.endsWith('/metro/live/stream')).length;
 await page.waitForTimeout(6000);
 expect(requests.filter(p=>p.endsWith('/metro/live/stream'))).toHaveLength(streams);
 expect(requests.some(p=>p.endsWith('/board')||p.endsWith('/board/calls'))).toBe(false);
});
