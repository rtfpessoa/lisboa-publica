import {useEffect,useRef,useState} from 'react';
import * as api from './api';

export function directionSelection(direction:api.BoardDirection|undefined){return direction?`${direction.line_key}|${direction.direction_key??'unknown'}`:''}
export type StationFrame={board:api.StopBoard;active?:api.BoardDirection;calls?:api.StopCallPage;offset:number;missing:boolean};
type Selection={direction:string;offset:number};

// A board and its selected calls are one visible frame. Revisions and source
// coverage never enter the UI until the matching selected page is ready.
export function useStationBoard(stopId:string,requestOptions:(signal:AbortSignal)=>NonNullable<Parameters<typeof api.getStopBoard>[2]>,prepare:(preserve:boolean)=>void){
 const [frame,setFrame]=useState<StationFrame>(),[selection,setSelection]=useState<Selection>({direction:'',offset:0}),[error,setError]=useState<unknown>(),[loading,setLoading]=useState(true);
 const current=useRef(frame),selected=useRef(selection),generation=useRef(0);current.current=frame;
 useEffect(()=>{
  const controller=new AbortController(),version=generation.current;let timer:ReturnType<typeof setTimeout>|undefined,offset=selection.offset;
  async function refresh(){
   setLoading(true);
   try{
    let next:StationFrame|undefined;
    for(let attempt=0;attempt<2;attempt++){
     const board=await api.getStopBoard(stopId,{},requestOptions(controller.signal));
     if(!Array.isArray(board.directions)||!board.coverage||!board.revision)throw new Error('Resposta de sentidos indisponível.');
     const requested=selected.current.direction||directionSelection(board.directions.find(d=>d.direction_key!=null)??board.directions[0]);
     const active=board.directions.find(d=>directionSelection(d)===requested);
     if(!active){next={board,active:current.current?.active,offset,missing:!!current.current?.active};break}
     try{
      const readPage=(pageOffset:number)=>api.listStopCalls(stopId,{lineKey:active.line_key,directionKey:active.direction_key??'unknown',limit:25,offset:pageOffset,revision:board.revision},requestOptions(controller.signal));
      let calls=await readPage(offset);
      if(calls.page&&offset>0&&offset>=calls.page.total){
       // Recover under the same frozen revision; never publish an empty obsolete page.
       const nearest=Math.floor(Math.max(0,calls.page.total-1)/25)*25;
       calls=await readPage(nearest);
      }
      if(!calls.coverage||!calls.page||!Array.isArray(calls.data))throw new Error('Resposta de horários indisponível.');
      next={board,active,calls,offset:calls.page.offset,missing:false};break;
     }catch(cause){if((cause as {status?:number}).status!==410||attempt===1)throw cause}
    }
    if(controller.signal.aborted||version!==generation.current||!next)return;
    if(current.current)prepare(directionSelection(current.current.active)===directionSelection(next.active)&&(current.current.offset===next.offset||next.offset!==offset));
    if(next.active)selected.current={direction:directionSelection(next.active),offset:next.offset};
    offset=next.offset;
    current.current=next;setFrame(next);setError(undefined);
   }catch(cause){if(!controller.signal.aborted&&version===generation.current)setError(cause)}
   finally{if(!controller.signal.aborted&&version===generation.current){setLoading(false);timer=setTimeout(refresh,5000)}}
  }
  void refresh();
  return()=>{controller.abort();clearTimeout(timer)};
 },[stopId,selection,requestOptions,prepare]);
 function choose(direction:string,offset=0){const next={direction,offset};generation.current++;selected.current=next;setSelection(next)}
 return {frame,error,loading,choose};
}
