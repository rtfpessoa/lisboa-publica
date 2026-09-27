import {useCallback,useRef,type RefObject} from 'react';

type RowPosition={id:string;top:number};
type Reading={panel:HTMLElement;scroll:number;rows:RowPosition[];anchor:number;focus?:{row:string;index:number}};

// Capture immediately before replacing a complete frame, while the previous DOM
// still exists. Restore before paint, using stable call identities rather than
// row numbers, which change when a new result is inserted above the reader.
export function useStationReading(root:RefObject<HTMLDivElement|null>){
 const pending=useRef<Reading|null>(null),reset=useRef(false);
 const prepare=useCallback((preserve:boolean)=>{
  const element=root.current,panel=element?.closest<HTMLElement>('.detail-panel');
  if(!element||!panel)return;
  if(!preserve){reset.current=true;pending.current=null;return}
  const {top,bottom}=panel.getBoundingClientRect();
  const elements=[...element.querySelectorAll<HTMLElement>('[data-call-id]')];
  const rows=elements.map(el=>({id:el.dataset.callId!,top:el.getBoundingClientRect().top}));
  const focused=document.activeElement,focusedRow=focused?.closest<HTMLElement>('[data-call-id]');
  const visible=(el:HTMLElement)=>el.getBoundingClientRect().bottom>top+Math.min(16,el.clientHeight/2)&&el.getBoundingClientRect().top<bottom;
  const anchor=focusedRow&&visible(focusedRow)?elements.indexOf(focusedRow):elements.findIndex(visible);
  const controls=focusedRow?[...focusedRow.querySelectorAll('a,button')]:[];
  pending.current={panel,scroll:panel.scrollTop,rows,anchor,focus:focusedRow?{row:focusedRow.dataset.callId!,index:controls.indexOf(focused!)}:undefined};
 },[root]);
 const restore=useCallback(()=>{
  const element=root.current;if(!element)return;
  if(reset.current){reset.current=false;element.querySelector('.direction-detail')?.scrollIntoView({block:'start'});return}
  const reading=pending.current;pending.current=null;if(!reading)return;
  const current=new Map([...element.querySelectorAll<HTMLElement>('[data-call-id]')].map(el=>[el.dataset.callId!,el]));
  const following=reading.anchor<0?[]:reading.rows.slice(reading.anchor),previous=reading.anchor<0?[]:reading.rows.slice(0,reading.anchor).reverse();
  const anchor=[...following,...previous].find(row=>current.has(row.id));
  if(anchor)reading.panel.scrollTop+=current.get(anchor.id)!.getBoundingClientRect().top-anchor.top;
  else reading.panel.scrollTop=reading.scroll;
  if(reading.focus){const row=current.get(reading.focus.row)??(anchor&&current.get(anchor.id));const target=row?.querySelectorAll<HTMLElement>('a,button')[Math.max(0,reading.focus.index)];target?.focus({preventScroll:true})}
 },[root]);
 return {prepare,restore};
}
