// THROWAWAY: timeline, table and next-stop focus on the existing dashboard route.
// npm run dev, then /?prototype=vehicle&variant=A. All data and actual events are simulated.
import {useEffect, useRef, useState} from 'react';
import './VehiclePopup.prototype.css';

type Variant='A'|'B'|'C';
type Case='normal'|'stale'|'unmatched'|'missing'|'repeat'|'progress';
type History='missing'|'reported'|'partial';
type Moment={at:string;kind:'Real'|'Previsão'|'Planeado'}|null;
type Scenario={id:string;operator:string;vehicle:string;line:string;estimated:boolean;stops:string[];initial:number};
const scenarios:Scenario[]=[
 {id:'metro',operator:'Metro',vehicle:'3B',line:'Vermelha',estimated:true,initial:6,stops:['São Sebastião','Saldanha','Alameda','Olaias','Bela Vista','Chelas','Olivais','Cabo Ruivo','Oriente','Moscavide','Encarnação','Aeroporto']},
 {id:'cp',operator:'CP',vehicle:'Exemplo 101',line:'Viagem nacional',estimated:false,initial:3,stops:['Lisboa Santa Apolónia','Lisboa Oriente','Vila Franca de Xira','Santarém','Entroncamento','Pombal','Coimbra-B','Aveiro','Espinho','Vila Nova de Gaia–Devesas','Porto Campanhã']},
 {id:'fertagus',operator:'Fertagus',vehicle:'Exemplo 102',line:'Fertagus',estimated:false,initial:4,stops:['Roma–Areeiro','Entrecampos','Sete Rios','Campolide','Pragal','Corroios','Foros de Amora','Fogueteiro','Coina','Penalva','Palmela','Venda do Alcaide','Setúbal']},
 {id:'carris',operator:'Carris',vehicle:'Exemplo 201',line:'Carreira de demonstração',estimated:false,initial:8,stops:Array.from({length:28},(_,i)=>`Paragem ${i+1}${i===8?' · ligação ao Metro':''}`)},
 {id:'cm',operator:'Carris Metropolitana',vehicle:'Exemplo 202',line:'Carreira de demonstração',estimated:false,initial:4,stops:['Terminal A','Paragem 2','Paragem 3','Estação','Paragem 5','Paragem 6','Paragem 7','Paragem 8','Terminal B']},
 {id:'tcb',operator:'TCB',vehicle:'Exemplo 203',line:'Carreira de demonstração',estimated:false,initial:3,stops:['Terminal fluvial','Paragem 2','Paragem 3','Paragem 4','Paragem 5','Paragem 6','Terminal B']},
 {id:'mobi',operator:'MobiCascais',vehicle:'Exemplo 204',line:'Carreira de demonstração',estimated:false,initial:3,stops:['Cascais','Paragem 2','Paragem 3','Paragem 4','Paragem 5','Paragem 6','Terminal B']},
 {id:'ttsl',operator:'TTSL',vehicle:'Exemplo 301',line:'Travessia de demonstração',estimated:false,initial:1,stops:['Cais do Sodré','Cacilhas']},
];
const variants:Variant[]=['A','B','C'];
const names={A:'Timeline completa',B:'Tabela do percurso',C:'Foco na próxima paragem'};
const cases:Record<Case,string>={normal:'Dados disponíveis',stale:'Fonte desatualizada',unmatched:'Viagem por confirmar',missing:'Sem próximos tempos',repeat:'Visita repetida',progress:'Progresso desconhecido'};
const histories:Record<History,string>={missing:'Sem eventos anteriores',reported:'Eventos reportados simulados',partial:'Eventos anteriores parciais'};
const params=new URLSearchParams(location.search);
const initialScenario=scenarios.find(s=>s.id===params.get('operator'))??scenarios[0];
const initialVariant=variants.includes(params.get('variant') as Variant)?params.get('variant') as Variant:'A';
const initialCase=params.get('case') as Case;
const initialHistory=params.get('history') as History;
const clock=(minutes:number)=>`${String(Math.floor(minutes/60)).padStart(2,'0')}:${String(minutes%60).padStart(2,'0')}`;

export default function VehiclePopupPrototype(){
 const [variant,setVariant]=useState<Variant>(initialVariant);
 const [scenario,setScenario]=useState(initialScenario);
 const [exampleCase,setExampleCase]=useState<Case>(initialCase in cases?initialCase:'normal');
 const [historyMode,setHistoryMode]=useState<History>(initialHistory in histories?initialHistory:'missing');
 const [reverse,setReverse]=useState(false);
 const [journey,setJourney]=useState(1);
 const [current,setCurrent]=useState(initialScenario.initial);
 const body=useRef<HTMLDivElement>(null);
 const stops=reverse?[...scenario.stops].reverse():[...scenario.stops];
 if(exampleCase==='repeat')stops.splice(stops.length-1,0,stops[1]);
 const now=870+(current-scenario.initial)*4;
 const progressKnown=exampleCase!=='progress'&&exampleCase!=='unmatched';
 const cycle=(delta:number)=>setVariant(v=>variants[(variants.indexOf(v)+delta+3)%3]);
 const focus=()=>body.current?.querySelector('[data-current=true]')?.scrollIntoView({block:'center'});
 const showStart=()=>{
  if(variant==='C'){
   const group=body.current?.querySelector<HTMLDetailsElement>('.vehicle-prototype-group');
   if(group)group.open=true;
   requestAnimationFrame(()=>body.current?.querySelector('[data-visit="1"]')?.scrollIntoView({block:'start'}));
  }else if(body.current)body.current.scrollTop=0;
 };
 useEffect(()=>{
  const url=new URL(location.href);
  url.searchParams.set('variant',variant);url.searchParams.set('operator',scenario.id);url.searchParams.set('case',exampleCase);url.searchParams.set('history',historyMode);
  window.history.replaceState(null,'',url);
 },[variant,scenario.id,exampleCase,historyMode]);
 useEffect(()=>{
  const frame=requestAnimationFrame(()=>{if(variant==='C'||!progressKnown){if(body.current)body.current.scrollTop=0}else focus()});
  return()=>cancelAnimationFrame(frame);
 },[variant,scenario.id,exampleCase,reverse,current]);
 useEffect(()=>{
  const key=(event:KeyboardEvent)=>{
   if(event.target instanceof HTMLElement&&event.target.closest('input,textarea,select,[contenteditable]'))return;
   if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();cycle(event.key==='ArrowLeft'?-1:1)}
  };
  window.addEventListener('keydown',key);return()=>window.removeEventListener('keydown',key);
 },[]);
 const moment=(index:number,departure=false):Moment=>{
  const planned=870+(index-scenario.initial)*4+3;
  if(departure&&index===stops.length-1)return null;
  if(index<current){
   if(historyMode==='missing'||(historyMode==='partial'&&index%3===0)||(departure&&index===current-1))return null;
   return {at:clock(planned+(departure?0:-1)),kind:'Real'};
  }
  if(exampleCase==='missing'||(departure&&index%4===1))return null;
  const prediction=exampleCase!=='stale'&&(departure?index%3===1:index%3!==2);
  return {at:clock(planned+(departure?1:0)+(prediction?1:0)),kind:prediction?'Previsão':'Planeado'};
 };
 const event=(value:Moment)=><span className={`vehicle-prototype-event ${value?.kind==='Real'?'vehicle-prototype-actual':''}`}><strong>{value?.at??'—'}</strong><small>{value?.kind??'Indisponível'}</small></span>;
 const phase=(index:number)=>!progressKnown?'Progresso por confirmar':index<current?(moment(index)?'Chegada reportada':'Passagem por confirmar'):index===current?(exampleCase==='stale'?'Última referência':scenario.estimated?'Próxima estimada':'Próxima paragem'):'Seguinte no percurso';
 const stopHeading=(index:number)=><div className="vehicle-prototype-stop-name"><strong>{stops[index]}</strong><small>Visita {index+1} · {phase(index)}</small></div>;
 const timelineRow=(index:number)=><article className={`vehicle-prototype-timeline-row ${index===current&&progressKnown?'vehicle-prototype-current':''}`} data-current={index===current&&progressKnown} data-visit={index+1} key={index}>
  <span className="vehicle-prototype-dot">{index+1}</span>{stopHeading(index)}<div><small>Chegada</small>{event(moment(index))}</div><div><small>Partida</small>{event(moment(index,true))}</div>
 </article>;
 const miniRow=(index:number)=><div className="vehicle-prototype-mini-row" data-visit={index+1} key={index}>{stopHeading(index)}<div><small>Chegada</small>{event(moment(index))}</div><div><small>Partida</small>{event(moment(index,true))}</div></div>;
 const indices=stops.map((_,i)=>i);
 const reset=(next:Scenario)=>{setScenario(next);setCurrent(next.initial);setReverse(false);setJourney(v=>v+1)};
 return <>
  <section className="vehicle-prototype-popup" aria-label="Protótipo do popup de veículo">
   <header>
    <div className="vehicle-prototype-label">PROTÓTIPO · TODOS OS TEMPOS E EVENTOS SÃO FICTÍCIOS</div>
    <div className="vehicle-prototype-heading"><div><small>{scenario.operator} · {scenario.line}</small><h2>Veículo {scenario.vehicle}</h2></div><span className="vehicle-prototype-destination">→ {stops.at(-1)}</span></div>
    <p className="vehicle-prototype-meta">{scenario.estimated?'Posição estimada':'Posição reportada'} · atualização da demonstração {clock(exampleCase==='stale'?now-20:now)}</p>
    <p className="vehicle-prototype-note">A sequência planeada não prova passagem. No passado, só eventos explicitamente reportados; «—» significa indisponível.</p>
    {exampleCase==='stale'&&<p className="vehicle-prototype-warning" role="status">Fonte desatualizada. Próximos tempos planeados; progresso atual por confirmar.</p>}
    {exampleCase==='progress'&&<p className="vehicle-prototype-warning" role="status">Percurso identificado, mas o progresso do veículo é desconhecido.</p>}
    {exampleCase!=='unmatched'&&<div className="vehicle-prototype-journey-toolbar"><strong>Percurso completo · {stops.length} visitas</strong><div><button onClick={showStart}>Ver início</button><button disabled={!progressKnown} onClick={()=>variant==='C'?body.current?.scrollTo({top:0}):focus()}>{exampleCase==='stale'?'Última referência':'Ver próxima'}</button></div></div>}
   </header>
   <div className="vehicle-prototype-body" ref={body}>
    {exampleCase==='unmatched'?<div className="vehicle-prototype-unmatched"><h3>Viagem por confirmar</h3><p>A linha e o sentido são conhecidos. O percurso e os horários deste veículo estão indisponíveis até identificarmos a sua viagem com segurança.</p></div>:<>
     {variant==='A'&&<div className="vehicle-prototype-timeline">{indices.map(timelineRow)}</div>}
     {variant==='B'&&<table className="vehicle-prototype-table"><thead><tr><th>Paragem / visita</th><th>Chegada</th><th>Partida</th></tr></thead><tbody>{indices.map(i=><tr className={i===current&&progressKnown?'vehicle-prototype-current':''} data-current={i===current&&progressKnown} data-visit={i+1} key={i}><td>{stopHeading(i)}</td><td>{event(moment(i))}</td><td>{event(moment(i,true))}</td></tr>)}</tbody></table>}
     {variant==='C'&&<>
      {progressKnown?<article className="vehicle-prototype-focus-card"><small>{exampleCase==='stale'?'Última referência de percurso':scenario.estimated?'Próxima paragem estimada':'Próxima paragem'}</small><h3>{stops[current]}</h3><div><div><small>Chegada</small>{event(moment(current))}</div><div><small>Partida</small>{event(moment(current,true))}</div></div></article>:<p className="vehicle-prototype-warning">Sem próxima paragem confirmada. Consulte a sequência completa abaixo.</p>}
      <details className="vehicle-prototype-group"><summary>{progressKnown?'Anteriores no percurso':'Primeiras visitas do percurso'} ({current})</summary>{indices.filter(i=>i<current).map(miniRow)}</details>
      <details className="vehicle-prototype-group" open><summary>{progressKnown?'Seguinte no percurso':'Restantes visitas do percurso'} ({stops.length-current-(progressKnown?1:0)})</summary>{indices.filter(i=>i>current||(!progressKnown&&i===current)).map(miniRow)}</details>
     </>}
     <small className="vehicle-prototype-coverage">Inclui todas as visitas da viagem, mesmo fora da região do mapa. «Real» é um evento reportado no cenário fictício, não uma garantia de disponibilidade do operador.</small>
    </>}
    <details className="vehicle-prototype-debug"><summary>Estado da demonstração</summary><pre>{JSON.stringify({variant,operator:scenario.id,exampleCase,historyMode,journey,reverse,current:progressKnown?current:null,simulatedClock:clock(now),visits:exampleCase==='unmatched'?null:indices.map(i=>({sequence:i+1,stop:stops[i],arrival:moment(i),departure:moment(i,true)}))},null,2)}</pre></details>
   </div>
  </section>
  <nav className="vehicle-prototype-switcher" aria-label="Controlos do protótipo de veículo">
   <div><button aria-label="Variante anterior" onClick={()=>cycle(-1)}>←</button><strong>{variant} · {names[variant]}</strong><button aria-label="Variante seguinte" onClick={()=>cycle(1)}>→</button></div>
   <label>Operador<select aria-label="Operador do cenário" value={scenario.id} onChange={e=>reset(scenarios.find(s=>s.id===e.target.value)!)}>{scenarios.map(s=><option key={s.id} value={s.id}>{s.operator}</option>)}</select></label>
   <label>Caso<select aria-label="Caso do cenário" value={exampleCase} onChange={e=>setExampleCase(e.target.value as Case)}>{Object.entries(cases).map(([key,value])=><option key={key} value={key}>{value}</option>)}</select></label>
   <label>Histórico simulado<select aria-label="Histórico simulado" value={historyMode} onChange={e=>setHistoryMode(e.target.value as History)}>{Object.entries(histories).map(([key,value])=><option key={key} value={key}>{value}</option>)}</select></label>
   <div><button disabled={!progressKnown||exampleCase==='stale'||current>=stops.length-1} onClick={()=>setCurrent(v=>v+1)}>Avançar referência</button><button onClick={()=>{setReverse(v=>!v);setCurrent(scenario.initial);setJourney(v=>v+1)}}>Inverter sentido</button></div>
  </nav>
 </>;
}
