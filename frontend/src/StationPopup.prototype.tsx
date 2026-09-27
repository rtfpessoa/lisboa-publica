// THROWAWAY: three station-popup layouts on the existing dashboard route.
// Run npm run dev, then /?prototype=station&variant=C. All fixture data is fictitious.
import {useEffect, useState} from 'react';
import './StationPopup.prototype.css';

type Direction = {name:string; destinations?:string[]};
type Line = {id:string; name:string; color:string; directions:Direction[]};
type Scenario = {id:string; operator:string; station:string; lines:Line[]};
type SourceState = 'current'|'empty'|'stale'|'unknown';
type Variant = 'A'|'B'|'C';
const green:Line={id:'verde',name:'Verde',color:'#20834b',directions:[{name:'Telheiras'},{name:'Cais do Sodré'}]};
const red:Line={id:'vermelha',name:'Vermelha',color:'#bc3243',directions:[{name:'São Sebastião'},{name:'Aeroporto'}]};
const yellow:Line={id:'amarela',name:'Amarela',color:'#aa7900',directions:[{name:'Odivelas',destinations:['Odivelas','Campo Grande']},{name:'Rato'}]};
const blue:Line={id:'azul',name:'Azul',color:'#2357a6',directions:[{name:'Reboleira'},{name:'Santa Apolónia'}]};
const scenarios:Scenario[]=[
 {id:'metro',operator:'Metro',station:'Alameda',lines:[green,red]},
 {id:'curto',operator:'Metro',station:'Marquês de Pombal · serviço curto',lines:[yellow,blue]},
 {id:'terminal',operator:'Metro',station:'Aeroporto · estação terminal',lines:[red]},
 {id:'cp',operator:'CP',station:'Lisboa Oriente',lines:[{id:'azambuja',name:'Azambuja',color:'#257449',directions:[{name:'Azambuja'},{name:'Lisboa Santa Apolónia'}]},{id:'sintra',name:'Sintra',color:'#35729d',directions:[{name:'Sintra'},{name:'Alverca'}]}]},
 {id:'fertagus',operator:'Fertagus',station:'Entrecampos',lines:[{id:'fertagus',name:'Fertagus',color:'#245ba1',directions:[{name:'Setúbal',destinations:['Setúbal','Coina']},{name:'Roma–Areeiro'}]}]},
 {id:'carris',operator:'Carris',station:'Paragem de demonstração',lines:[{id:'744',name:'744',color:'#9b7100',directions:[{name:'Oriente'},{name:'Marquês de Pombal'}]},{id:'736',name:'736',color:'#9b7100',directions:[{name:'Odivelas'},{name:'Cais do Sodré'}]},{id:'783',name:'783',color:'#9b7100',directions:[{name:'Portela'},{name:'Amoreiras'}]}]},
 {id:'cm',operator:'Carris Metropolitana',station:'Paragem de demonstração',lines:[{id:'1001',name:'1001',color:'#77409b',directions:[{name:'Reboleira'},{name:'Destino oposto'}]},{id:'1002',name:'1002',color:'#77409b',directions:[{name:'Terminal A'},{name:'Terminal B'}]}]},
 {id:'tcb',operator:'TCB',station:'Paragem de demonstração',lines:[{id:'tcb-a',name:'Linha A',color:'#225c9f',directions:[{name:'Terminal fluvial'},{name:'Destino oposto'}]}]},
 {id:'mobi',operator:'MobiCascais',station:'Paragem de demonstração',lines:[{id:'mobi-a',name:'Linha A',color:'#086f77',directions:[{name:'Cascais'},{name:'Destino oposto'}]}]},
 {id:'circular',operator:'Autocarro · caso circular',station:'Paragem de demonstração',lines:[{id:'circular',name:'Circular',color:'#77533a',directions:[{name:'Circular · sentido publicado'}]}]},
 {id:'ttsl',operator:'TTSL',station:'Cais do Sodré',lines:[{id:'cacilhas',name:'Cacilhas',color:'#157781',directions:[{name:'Cacilhas'},{name:'Cais do Sodré'}]},{id:'seixal',name:'Seixal',color:'#157781',directions:[{name:'Seixal'},{name:'Cais do Sodré'}]}]},
];
const variants:Variant[]=['A','B','C'];
const variantNames={A:'Linha → sentido',B:'Linhas expansíveis',C:'Grelha de sentidos'};
const stateNames={current:'Atual',empty:'Sem chegadas',stale:'Fonte desatualizada',unknown:'Destino desconhecido'};
const params=new URLSearchParams(location.search);
const initialVariant=variants.includes(params.get('variant') as Variant)?params.get('variant') as Variant:'C';
const initialScenario=scenarios.find(s=>s.id===params.get('scenario'))??scenarios[0];
const initialState=Object.keys(stateNames).includes(params.get('state')??'')?params.get('state') as SourceState:'current';
const count=(direction:number,state:SourceState)=>state==='empty'?0:direction===0?12:3;
const clock=(seconds:number)=>`${String(Math.floor(seconds/60)).padStart(2,'0')}:${String(seconds%60).padStart(2,'0')}`;

export default function StationPopupPrototype(){
 const [variant,setVariant]=useState<Variant>(initialVariant);
 const [scenario,setScenario]=useState(initialScenario);
 const [sourceState,setSourceState]=useState<SourceState>(initialState);
 const [lineId,setLineId]=useState(initialScenario.lines[0].id);
 const [direction,setDirection]=useState(0);
 const [visible,setVisible]=useState(5);
 const [openLines,setOpenLines]=useState<string[]>([initialScenario.lines[0].id]);
 const [lineDirections,setLineDirections]=useState<Record<string,number>>({});
 const [narrow,setNarrow]=useState(false);
 const line=scenario.lines.find(l=>l.id===lineId)??scenario.lines[0];
 const select=(next:Line,index=0)=>{setLineId(next.id);setDirection(index);setLineDirections(previous=>({...previous,[next.id]:index}));setVisible(5)};
 const recordCount=(index:number)=>scenario.id==='terminal'&&index===1?0:count(index,sourceState);
 const cycle=(delta:number)=>setVariant(v=>variants[(variants.indexOf(v)+delta+variants.length)%variants.length]);
 useEffect(()=>{
  const url=new URL(location.href);
  url.searchParams.set('variant',variant);url.searchParams.set('scenario',scenario.id);url.searchParams.set('state',sourceState);
  history.replaceState(null,'',url);
 },[variant,scenario,sourceState]);
 useEffect(()=>{
  const key=(event:KeyboardEvent)=>{
   if(event.target instanceof HTMLElement&&event.target.closest('input,textarea,select,[contenteditable], [role=tablist]'))return;
   if(event.key==='ArrowLeft'||event.key==='ArrowRight'){event.preventDefault();cycle(event.key==='ArrowLeft'?-1:1)}
  };
  window.addEventListener('keydown',key);return()=>window.removeEventListener('keydown',key);
 },[]);
 const directionTabs=(selected:Line)=><div className="station-prototype-directions" role="tablist" aria-label={`Sentidos · ${selected.name}`}>
  {selected.directions.map((d,i)=><button key={d.name} role="tab" aria-selected={(lineDirections[selected.id]??0)===i} onClick={()=>select(selected,i)}>
   <span>{d.name}</span><small>{recordCount(i)} registos</small>
  </button>)}
 </div>;
 const rows=(selected:Line,index:number,compactDestination=false)=>{
  const total=recordCount(index),destination=selected.directions[index];
  if(!total)return <div className="station-prototype-empty">{scenario.id==='terminal'&&index===1?'Este sentido termina aqui. Sem serviço seguinte para este destino.':'Sem chegadas disponíveis para este sentido.'}<small>A linha e o sentido continuam visíveis.</small></div>;
  return <div className="station-prototype-rows" role={compactDestination?'region':'tabpanel'} aria-label={`${selected.name} · ${destination.name}`}>
   <div className="station-prototype-row station-prototype-column-labels"><span>{compactDestination?'Viagem':'Destino da viagem'}</span><span>Chegada</span><span>Partida</span></div>
   {Array.from({length:Math.min(visible,total)},(_,i)=>{
    const planned=i%3===2,arrival=870+3+i*4+index,departure=arrival+1;
    const dest=destination.destinations?.[i%(destination.destinations.length)]??destination.name;
    return <div className="station-prototype-row" key={`${selected.id}-${index}-${i}`}>
     <div><strong>{compactDestination?`Viagem ${i+1}`:dest}</strong>{compactDestination?(dest!==destination.name&&<small>Termina em {dest}</small>):<small>Viagem de demonstração {i+1}</small>}</div>
     <div><strong>{clock(arrival)}</strong><small>{planned?'Planeado':sourceState==='stale'?'Última previsão':'Previsão'}</small></div>
     <div><strong>{i%2===0?clock(departure):'—'}</strong><small>{i%2===0?(planned?'Planeado':sourceState==='stale'?'Última previsão':'Previsão'):'Indisponível'}</small></div>
    </div>;
   })}
   {visible<total&&<button className="station-prototype-more" onClick={()=>setVisible(v=>v+5)}>Mais chegadas neste sentido ({total-Math.min(visible,total)})</button>}
   <small className="station-prototype-footnote">{Math.min(visible,total)} de {total} neste sentido · dados fictícios, relógio fixo 14:30</small>
  </div>;
 };
 return <>
  <section className={`station-prototype-popup ${narrow?'station-prototype-narrow':''}`} aria-label="Protótipo do popup de estação">
   <div className="station-prototype-label">PROTÓTIPO · DADOS FICTÍCIOS · {variantNames[variant]}</div>
   <header><div><small>{scenario.operator}</small><h2>{scenario.station}</h2></div><span className="station-prototype-update">{sourceState==='stale'?'Última atualização 14:10':'Atualização de demonstração 14:30'}</span></header>
   <p className="station-prototype-disclaimer">Nomes, serviços e tempos servem apenas para comparar a interação.</p>
   {sourceState==='stale'&&<p className="station-prototype-warning" role="status">Fonte desatualizada. As horas abaixo são previsões anteriores; a chegada atual está por confirmar.</p>}
   {variant==='A'&&<>
    <div className="station-prototype-lines" role="tablist" aria-label="Linhas na estação">{scenario.lines.map(l=><button role="tab" aria-selected={l.id===line.id} key={l.id} onClick={()=>select(l)}><i style={{background:l.color}}/>{l.name}</button>)}</div>
    {directionTabs(line)}{rows(line,direction)}
   </>}
   {variant==='B'&&<div className="station-prototype-accordions">{scenario.lines.map(l=>{
    const open=openLines.includes(l.id);
    return <article key={l.id}>
     <button className="station-prototype-accordion-heading" aria-expanded={open} onClick={()=>{
      setOpenLines(ids=>open?ids.filter(id=>id!==l.id):[...ids,l.id]);select(l);
     }}><span><i style={{background:l.color}}/>{l.name}</span><span>{l.directions.map((d,i)=>`${d.name}: ${recordCount(i)}`).join(' · ')} {open?'−':'+'}</span></button>
     {open&&<>{directionTabs(l)}{rows(l,lineDirections[l.id]??0)}</>}
    </article>;
   })}</div>}
   {variant==='C'&&<>
    <div className="station-prototype-matrix">{scenario.lines.map(l=><article key={l.id}>
     <h3><i style={{background:l.color}}/>{l.name}</h3>
     <div>{l.directions.map((d,i)=><button className={l.id===line.id&&i===direction?'selected':''} aria-pressed={l.id===line.id&&i===direction} aria-controls="station-prototype-selected-detail" key={d.name} onClick={()=>select(l,i)}>
      <strong>{d.name}</strong><span>{!recordCount(i)?'Sem chegadas':sourceState==='stale'?'Previsão anterior':`${3+i} min · previsão`}</span><small>{recordCount(i)} registos · abrir</small>
     </button>)}</div>
    </article>)}</div>
    <div id="station-prototype-selected-detail" className="station-prototype-matrix-detail"><h3>Próximas chegadas e partidas <span className="station-prototype-selected-direction" aria-live="polite">→ {line.directions[direction].name}</span></h3>{rows(line,direction,true)}</div>
   </>}
   {sourceState==='unknown'&&<details className="station-prototype-unknown" open><summary>1 chegada sem sentido identificado</summary><p>Destino indisponível · chegada 14:36, previsão · partida indisponível.</p><small>Fica separada dos sentidos conhecidos; não é atribuída por aproximação.</small></details>}
   <details className="station-prototype-debug"><summary>Estado da demonstração</summary><pre>{JSON.stringify({variant,scenario:scenario.id,sourceState,line:line.id,direction:line.directions[direction].name,visible,openLines,lineDirections,narrow,recordsPerDirection:line.directions.map((d,i)=>({direction:d.name,count:recordCount(i)}))},null,2)}</pre></details>
  </section>
  <nav className="station-prototype-switcher" aria-label="Controlos do protótipo">
   <div><button onClick={()=>cycle(-1)} aria-label="Variante anterior">←</button><strong>{variant} · {variantNames[variant]}</strong><button onClick={()=>cycle(1)} aria-label="Variante seguinte">→</button></div>
   <label>Cenário<select aria-label="Cenário" value={scenario.id} onChange={e=>{
    const next=scenarios.find(s=>s.id===e.target.value)!;setScenario(next);select(next.lines[0]);setOpenLines([next.lines[0].id]);
   }}>{scenarios.map(s=><option key={s.id} value={s.id}>{s.operator} · {s.station}</option>)}</select></label>
   <label>Fonte<select aria-label="Fonte" value={sourceState} onChange={e=>{setSourceState(e.target.value as SourceState);setVisible(5)}}>{Object.entries(stateNames).map(([key,value])=><option key={key} value={key}>{value}</option>)}</select></label>
   <button aria-pressed={narrow} onClick={()=>setNarrow(v=>!v)}>Vista estreita</button>
  </nav>
 </>;
}
